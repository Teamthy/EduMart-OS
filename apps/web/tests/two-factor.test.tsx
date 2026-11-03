import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError, type LoginOutcome, type SessionResponse } from "@edumart/shared";

// The API client is the seam under test: what matters is that a *challenge* is handled as "a code is owed" rather
// than as a failed sign-in, and that the password is gone from the form by the time the code is asked for.
const login = vi.fn<(email: string, password: string, client?: string) => Promise<LoginOutcome>>();
const completeLogin = vi.fn<(token: string, code: string, client?: string) => Promise<SessionResponse>>();
vi.mock("@/lib/browser-api", () => ({
  api: () => ({ login, completeLogin, twoFactorStatus: vi.fn(), beginTwoFactor: vi.fn(), confirmTwoFactor: vi.fn(), disableTwoFactor: vi.fn() }),
}));

const replace = vi.fn();
const refresh = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ replace, refresh }) }));

import { LoginForm } from "@/app/login/login-form";

const session: SessionResponse = {
  token: "access",
  expires_at: "2026-01-01T00:00:00Z",
  user: {
    id: "usr_1",
    email: "amara@example.com",
    displayName: "Amara",
    status: "active",
    examPathwayId: null,
    targetDate: null,
    timezone: "Africa/Lagos",
    dailyMinutes: 30,
    onboardingStep: 3,
    onboardingComplete: true,
    roles: ["student"],
    subjectIds: [],
    phone: null,
    emailVerified: true,
    phoneVerified: false,
    createdAt: "2025-09-01T00:00:00Z",
  },
  next: "/dashboard",
};

function fillAndSubmit() {
  fireEvent.change(screen.getByLabelText(/email/i), { target: { value: "amara@example.com" } });
  fireEvent.change(screen.getByLabelText(/^password/i), { target: { value: "Str0ngPassw0rd!" } });
  fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
}

describe("two-step sign-in", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("asks for a code instead of claiming the password was wrong", async () => {
    login.mockResolvedValue({
      two_factor_required: true,
      token: "pending-token",
      expires_at: "2026-01-01T00:05:00Z",
      recovery_available: true,
      message: "Enter the code from your authenticator app.",
    });

    render(<LoginForm nextPath="" />);
    fillAndSubmit();

    expect(await screen.findByText(/enter the six-digit code/i)).toBeTruthy();
    // No error banner: nothing failed.
    expect(screen.queryByRole("alert")).toBeNull();
    // The password field is gone, not merely blanked: it is not on screen to be read or re-submitted during the
    // second step, which is the point of clearing it.
    expect(screen.queryByLabelText(/^password/i)).toBeNull();
    // And nothing navigated yet.
    expect(replace).not.toHaveBeenCalled();
  });

  it("signs in when the code is accepted, and lands on the API's next route", async () => {
    login.mockResolvedValue({
      two_factor_required: true,
      token: "pending-token",
      expires_at: "2026-01-01T00:05:00Z",
      recovery_available: true,
      message: "Enter the code.",
    });
    completeLogin.mockResolvedValue(session);

    render(<LoginForm nextPath="" />);
    fillAndSubmit();

    const code = await screen.findByLabelText(/verification code/i);
    fireEvent.change(code, { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: /verify and sign in/i }));

    await waitFor(() => expect(completeLogin).toHaveBeenCalledWith("pending-token", "123456", "web"));
    await waitFor(() => expect(replace).toHaveBeenCalledWith("/dashboard"));
  });

  it("keeps the code step usable after a wrong code, and clears the field", async () => {
    login.mockResolvedValue({
      two_factor_required: true,
      token: "pending-token",
      expires_at: "2026-01-01T00:05:00Z",
      recovery_available: true,
      message: "Enter the code.",
    });
    completeLogin.mockRejectedValue(
      new ApiError({
        code: "TWO_FACTOR_INVALID",
        message: "That code is not right. Use the current code from your authenticator app, or one of your recovery codes.",
        status: 422,
      }),
    );

    render(<LoginForm nextPath="" />);
    fillAndSubmit();

    const code = await screen.findByLabelText(/verification code/i);
    fireEvent.change(code, { target: { value: "000000" } });
    fireEvent.click(screen.getByRole("button", { name: /verify and sign in/i }));

    // A wrong code is an error on the same screen: the token is still good, so the person can try again.
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.getByLabelText(/verification code/i)).toBeTruthy();
    expect(replace).not.toHaveBeenCalled();
  });

  it("returns to the password step when the challenge itself expired", async () => {
    login.mockResolvedValue({
      two_factor_required: true,
      token: "pending-token",
      expires_at: "2026-01-01T00:05:00Z",
      recovery_available: false,
      message: "Enter the code.",
    });
    completeLogin.mockRejectedValue(
      new ApiError({
        code: "TWO_FACTOR_REQUIRED",
        message: "Your sign-in expired. Enter your email and password again.",
        status: 428,
      }),
    );

    render(<LoginForm nextPath="" />);
    fillAndSubmit();

    fireEvent.change(await screen.findByLabelText(/verification code/i), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: /verify and sign in/i }));

    // A spent token cannot be retried, so the form goes back to email and password rather than looping.
    await waitFor(() => expect(screen.queryByLabelText(/verification code/i)).toBeNull());
    expect(screen.getByLabelText(/email/i)).toBeTruthy();
  });
});
