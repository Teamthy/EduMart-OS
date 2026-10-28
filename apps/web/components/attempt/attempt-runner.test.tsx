import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AssessmentSessionView } from "@edumart/shared";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: vi.fn(), refresh: vi.fn(), push: vi.fn() }),
}));

import { AttemptRunner } from "./attempt-runner";

const session: AssessmentSessionView = {
  session_id: "sess_1",
  mode: "practice",
  status: "in_progress",
  attempt_no: 1,
  started_at: new Date().toISOString(),
  deadline_at: new Date(Date.now() + 600_000).toISOString(),
  submitted_at: null,
  server_time: new Date().toISOString(),
  question_count: 2,
  result_released: false,
  remaining_seconds: 600,
  score: null,
  questions: [
    {
      qvId: "qv_1",
      stem: "What is 2 + 2?",
      options: [
        { id: "opt_1", label: "A. 3" },
        { id: "opt_2", label: "B. 4" },
      ],
      position: 1,
      selectedOptionId: null,
      answerVersion: 0,
    },
    {
      qvId: "qv_2",
      stem: "What is the capital of Nigeria?",
      options: [
        { id: "opt_1", label: "A. Lagos" },
        { id: "opt_2", label: "B. Abuja" },
      ],
      position: 2,
      selectedOptionId: null,
      answerVersion: 0,
    },
  ],
};

function jsonResponse(body: unknown, ok = true) {
  return Promise.resolve({
    ok,
    status: ok ? 200 : 409,
    text: async () => JSON.stringify(body),
  } as Response);
}

describe("AttemptRunner save states", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("shows a saving state, then confirms the answer is on the server", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({ saved: true, server_saved_at: new Date().toISOString(), version: 1, session_status: "in_progress", remaining_seconds: 590 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<AttemptRunner session={session} />);
    const user = userEvent.setup();

    expect(screen.getByText(/answers save as soon as you choose them/i)).toBeDefined();

    await user.click(screen.getByLabelText("B. 4"));

    await waitFor(() => {
      expect(screen.getByText(/all answers saved on the server/i)).toBeDefined();
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("never claims an answer is saved when the request fails", async () => {
    const fetchMock = vi.fn(async () => {
      throw new TypeError("Failed to fetch");
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AttemptRunner session={session} />);
    const user = userEvent.setup();
    await user.click(screen.getByLabelText("A. 3"));

    await waitFor(() => {
      expect(screen.getByText(/not saved/i)).toBeDefined();
    });
    expect(screen.queryByText(/all answers saved on the server/i)).toBeNull();
  });

  it("does not render the answer key before release", () => {
    vi.stubGlobal("fetch", vi.fn());
    const { container } = render(<AttemptRunner session={session} />);
    expect(container.innerHTML).not.toMatch(/answerOptionId|explanation/i);
  });

  it("blocks submission while an answer is still saving", async () => {
    let resolveFetch: ((value: Response) => void) | undefined;
    const fetchMock = vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          resolveFetch = resolve;
        }),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(<AttemptRunner session={session} />);
    const user = userEvent.setup();
    await user.click(screen.getByLabelText("A. 3"));

    const submit = screen.getByRole("button", { name: /submit attempt/i }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);

    resolveFetch?.({
      ok: true,
      status: 200,
      text: async () =>
        JSON.stringify({ saved: true, server_saved_at: new Date().toISOString(), version: 1, session_status: "in_progress", remaining_seconds: 580 }),
    } as Response);

    await waitFor(() => expect((screen.getByRole("button", { name: /submit attempt/i }) as HTMLButtonElement).disabled).toBe(false));
  });
});
