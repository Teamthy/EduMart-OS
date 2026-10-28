import { randomBytes } from "node:crypto";
import { q, id, nowIso, transaction } from "./db";
import { ApiError } from "./http";
import { sha256, type AuthUser } from "./auth";
import { audit } from "./audit";

/**
 * Classroom + tenant isolation. Every read/write is scoped by an explicit server-side
 * membership check; filtering in the UI is never treated as authorization.
 */

export type ClassScope = {
  id: string;
  name: string;
  join_code: string;
  organization_id: string | null;
  owner_user_id: string;
  subject_id: string | null;
  role: "teacher" | "student" | null;
};

export function classForUser(userId: string, classId: string): ClassScope | null {
  const cls = q.get<any>("SELECT * FROM classes WHERE id = ?", classId);
  if (!cls) return null;
  const membership = q.get<any>(
    "SELECT role FROM class_memberships WHERE class_id = ? AND user_id = ? AND status = 'active'",
    classId,
    userId,
  );
  if (!membership) return null;
  return { ...cls, role: membership.role === "teacher" ? "teacher" : "student" };
}

export function requireClassTeacher(user: AuthUser, classId: string): ClassScope {
  const scope = classForUser(user.id, classId);
  if (!scope) throw new ApiError("NOT_FOUND", "That class could not be found.");
  const isOwner = scope.owner_user_id === user.id;
  const isPlatformAdmin = user.roles.includes("platform_admin");
  if (scope.role !== "teacher" && !isOwner && !isPlatformAdmin) {
    throw new ApiError("FORBIDDEN", "Only a teacher of this class can perform that action.");
  }
  return scope;
}

export function listClasses(userId: string) {
  const rows = q.all<any>(
    `SELECT c.id, c.name, c.join_code, c.organization_id, c.owner_user_id, cm.role,
            o.name AS organization_name,
            (SELECT COUNT(*) FROM class_memberships m WHERE m.class_id = c.id AND m.role = 'student' AND m.status = 'active') AS student_count,
            (SELECT COUNT(*) FROM assessment_definitions d WHERE d.class_id = c.id AND d.status = 'published') AS assessment_count
       FROM classes c
       JOIN class_memberships cm ON cm.class_id = c.id AND cm.user_id = ? AND cm.status = 'active'
       LEFT JOIN organizations o ON o.id = c.organization_id
      WHERE c.status = 'active'
      ORDER BY c.created_at DESC`,
    userId,
  );
  return rows;
}

export function createClass(user: AuthUser, input: { name: string; organizationId?: string | null; subjectId?: string | null }) {
  const name = input.name.trim();
  if (name.length < 3) throw new ApiError("VALIDATION_ERROR", "Give the class a name of at least 3 characters.");

  // Tenant check: a teacher may only create classes inside an organization they belong to.
  if (input.organizationId) {
    const membership = q.get<any>(
      "SELECT role FROM organization_memberships WHERE organization_id = ? AND user_id = ? AND status = 'active'",
      input.organizationId,
      user.id,
    );
    if (!membership) throw new ApiError("FORBIDDEN", "You are not a member of that organization.");
  }

  const classId = id();
  const joinCode = `EDU-${randomBytes(3).toString("hex").toUpperCase()}`;
  transaction(() => {
    q.run(
      "INSERT INTO classes (id, organization_id, owner_user_id, name, subject_id, join_code, status, created_at) VALUES (?,?,?,?,?,?, 'active', ?)",
      classId,
      input.organizationId ?? null,
      user.id,
      name,
      input.subjectId ?? null,
      joinCode,
      nowIso(),
    );
    q.run(
      "INSERT INTO class_memberships (id, class_id, user_id, role, status, joined_at) VALUES (?,?,?,'teacher','active',?)",
      id(),
      classId,
      user.id,
      nowIso(),
    );
    if (input.organizationId) {
      const existing = q.get<any>(
        "SELECT id FROM organization_memberships WHERE organization_id = ? AND user_id = ?",
        input.organizationId,
        user.id,
      );
      if (!existing) {
        q.run(
          "INSERT INTO organization_memberships (id, organization_id, user_id, role, status, joined_at) VALUES (?,?,?, 'teacher','active',?)",
          id(),
          input.organizationId,
          user.id,
          nowIso(),
        );
      }
    }
  });
  audit({
    actorUserId: user.id,
    action: "class.created",
    resourceType: "class",
    resourceId: classId,
    metadata: { name },
  });
  return classForUser(user.id, classId);
}

export function classMembers(user: AuthUser, classId: string) {
  const scope = classForUser(user.id, classId);
  if (!scope) throw new ApiError("NOT_FOUND", "That class could not be found.");
  const members = q.all<any>(
    `SELECT u.id, u.display_name, u.email_normalized, cm.role, cm.joined_at,
            (SELECT COUNT(*) FROM assessment_sessions s WHERE s.user_id = u.id AND s.class_id = ? AND s.status != 'in_progress') AS attempts,
            (SELECT COALESCE(ROUND(AVG(s.score_percent)), 0) FROM assessment_sessions s
              WHERE s.user_id = u.id AND s.class_id = ? AND s.status != 'in_progress') AS average
       FROM class_memberships cm JOIN users u ON u.id = cm.user_id
      WHERE cm.class_id = ? AND cm.status = 'active' ORDER BY cm.role DESC, u.display_name`,
    classId,
    classId,
    classId,
  );
  return { scope, members };
}

export function createInvitation(user: AuthUser, input: { classId: string; email?: string; role?: "student" | "teacher"; maxUses?: number }) {
  const scope = requireClassTeacher(user, input.classId);
  const token = randomBytes(24).toString("base64url");
  const expiresAt = new Date(Date.now() + 7 * 86400000).toISOString();
  q.run(
    `INSERT INTO invitations (id, organization_id, class_id, email, token_hash, invited_by, intended_role, expires_at,
       accepted_at, accepted_by, max_uses, use_count, created_at)
     VALUES (?,?,?,?,?,?,?,?,NULL,NULL,?,0,?)`,
    id(),
    scope.organization_id,
    input.classId,
    input.email ?? null,
    sha256(token),
    user.id,
    input.role ?? "student",
    expiresAt,
    Math.min(Math.max(input.maxUses ?? 1, 1), 200),
    nowIso(),
  );
  audit({
    actorUserId: user.id,
    action: "invitation.created",
    resourceType: "class",
    resourceId: input.classId,
    metadata: { role: input.role ?? "student", maxUses: input.maxUses ?? 1 },
  });
  return { token, expiresAt, joinPath: `/join/${token}` };
}

export function acceptInvitation(user: AuthUser, token: string) {
  const invitation = q.get<any>("SELECT * FROM invitations WHERE token_hash = ?", sha256(token));
  if (!invitation) throw new ApiError("NOT_FOUND", "That invitation link is not valid.");
  if (invitation.accepted_at && invitation.use_count >= invitation.max_uses) {
    throw new ApiError("CONFLICT", "That invitation has already been used.");
  }
  if (new Date(invitation.expires_at).getTime() < Date.now()) {
    throw new ApiError("CONFLICT", "That invitation has expired. Ask your teacher for a new link.");
  }
  if (invitation.use_count >= invitation.max_uses) {
    throw new ApiError("CONFLICT", "That invitation has reached its usage limit.");
  }
  if (invitation.email && invitation.email.toLowerCase() !== user.email) {
    throw new ApiError("FORBIDDEN", "This invitation was issued for a different email address.");
  }
  if (!invitation.class_id) throw new ApiError("CONFLICT", "This invitation is not linked to a class.");

  transaction(() => {
    q.run(
      "UPDATE invitations SET use_count = use_count + 1, accepted_at = COALESCE(accepted_at, ?), accepted_by = ? WHERE id = ?",
      nowIso(),
      user.id,
      invitation.id,
    );
    const existing = q.get<any>(
      "SELECT id FROM class_memberships WHERE class_id = ? AND user_id = ?",
      invitation.class_id,
      user.id,
    );
    if (existing) {
      q.run("UPDATE class_memberships SET status = 'active' WHERE id = ?", existing.id);
    } else {
      q.run(
        "INSERT INTO class_memberships (id, class_id, user_id, role, status, joined_at) VALUES (?,?,?,?,'active',?)",
        id(),
        invitation.class_id,
        user.id,
        invitation.intended_role === "teacher" ? "teacher" : "student",
        nowIso(),
      );
    }
    if (invitation.organization_id) {
      const orgMembership = q.get<any>(
        "SELECT id FROM organization_memberships WHERE organization_id = ? AND user_id = ?",
        invitation.organization_id,
        user.id,
      );
      if (!orgMembership) {
        q.run(
          "INSERT INTO organization_memberships (id, organization_id, user_id, role, status, joined_at) VALUES (?,?,?,'student','active',?)",
          id(),
          invitation.organization_id,
          user.id,
          nowIso(),
        );
      }
    }
  });
  audit({
    actorUserId: user.id,
    action: "invitation.accepted",
    resourceType: "class",
    resourceId: invitation.class_id,
  });
  return { classId: invitation.class_id as string };
}

export function joinClassByCode(user: AuthUser, code: string) {
  const cls = q.get<any>("SELECT * FROM classes WHERE join_code = ? AND status = 'active'", code.trim().toUpperCase());
  if (!cls) throw new ApiError("NOT_FOUND", "That class code was not recognised.");
  const existing = q.get<any>("SELECT id FROM class_memberships WHERE class_id = ? AND user_id = ?", cls.id, user.id);
  if (existing) {
    q.run("UPDATE class_memberships SET status = 'active' WHERE id = ?", existing.id);
  } else {
    q.run(
      "INSERT INTO class_memberships (id, class_id, user_id, role, status, joined_at) VALUES (?,?,?,'student','active',?)",
      id(),
      cls.id,
      user.id,
      nowIso(),
    );
  }
  audit({ actorUserId: user.id, action: "class.joined", resourceType: "class", resourceId: cls.id, metadata: { method: "code" } });
  return { classId: cls.id as string, name: cls.name as string };
}
