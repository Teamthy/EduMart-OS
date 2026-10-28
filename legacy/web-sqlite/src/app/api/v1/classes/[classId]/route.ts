import { handle, ok, ApiError } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { classForUser, classMembers } from "@/lib/server/classes";
import { classReport } from "@/lib/server/insights";

export async function GET(req: Request, ctx: { params: Promise<{ classId: string }> }) {
  return handle(req, async () => {
    const user = await requireUser();
    const { classId } = await ctx.params;
    const scope = classForUser(user.id, classId);
    if (!scope) throw new ApiError("NOT_FOUND", "That class could not be found.");

    const { members } = classMembers(user, classId);
    const isTeacher = scope.role === "teacher" || scope.owner_user_id === user.id || user.roles.includes("platform_admin");
    return ok({
      class: scope,
      members: isTeacher
        ? members
        : members.map((m) => ({ id: m.id, display_name: m.display_name, role: m.role })),
      report: isTeacher ? classReport(classId) : null,
    });
  });
}
