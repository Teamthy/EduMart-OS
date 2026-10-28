package domain

import "testing"

func question(qvID, answer string, points int) SnapQuestion {
	return SnapQuestion{
		QvID: qvID, AnswerOption: answer, Points: points, Position: 1,
		Options: []Option{{ID: "opt_1", Label: "A"}, {ID: "opt_2", Label: "B"}},
	}
}

func ptr(v string) *string { return &v }

func TestScoreAttemptDeterministic(t *testing.T) {
	questions := []SnapQuestion{question("q1", "opt_1", 1), question("q2", "opt_2", 1), question("q3", "opt_1", 2)}
	responses := []SnapResponse{
		{QvID: "q1", SelectedOptionID: ptr("opt_1")},
		{QvID: "q2", SelectedOptionID: ptr("opt_1")},
		{QvID: "q3"},
	}

	first := ScoreAttempt(questions, responses, PolicyV1)
	for i := 0; i < 20; i++ {
		again := ScoreAttempt(questions, responses, PolicyV1)
		if again.Raw != first.Raw || again.Max != first.Max || again.Percent != first.Percent {
			t.Fatalf("scoring is not deterministic: %+v vs %+v", first, again)
		}
	}

	if first.Correct != 1 || first.Incorrect != 1 || first.Unanswered != 1 {
		t.Fatalf("unexpected tally: %+v", first)
	}
	if first.Raw != 1 {
		t.Fatalf("expected raw 1 with one correct 1-point answer, got %d", first.Raw)
	}
	if first.Max != 4 {
		t.Fatalf("expected max 4 (1 + 1 + 2 points), got %d", first.Max)
	}
	if first.Percent != 25 {
		t.Fatalf("expected 25%%, got %d", first.Percent)
	}
}

func TestScoringPolicyVersionIsRecorded(t *testing.T) {
	questions := []SnapQuestion{question("q1", "opt_1", 1)}
	responses := []SnapResponse{{QvID: "q1", SelectedOptionID: ptr("opt_2")}}

	v1 := ScoreAttempt(questions, responses, PolicyV1)
	if v1.PolicyVersion != 1 || v1.Raw != 0 {
		t.Fatalf("policy v1 should not penalise: %+v", v1)
	}

	v2 := ScoreAttempt(questions, responses, PolicyV2)
	if v2.PolicyVersion != 2 {
		t.Fatalf("expected policy version 2 recorded, got %d", v2.PolicyVersion)
	}
	if v2.Raw != 0 || v2.Max != 2 {
		t.Fatalf("policy v2 doubles marks and floors the raw score at zero: %+v", v2)
	}
}

func TestScoreAttemptClampsNegativeRaw(t *testing.T) {
	questions := []SnapQuestion{question("q1", "opt_1", 1), question("q2", "opt_1", 1)}
	responses := []SnapResponse{
		{QvID: "q1", SelectedOptionID: ptr("opt_2")},
		{QvID: "q2", SelectedOptionID: ptr("opt_2")},
	}
	result := ScoreAttempt(questions, responses, PolicyV2)
	if result.Raw != 0 {
		t.Fatalf("negative raw scores are clamped to zero, got %d", result.Raw)
	}
	if result.Incorrect != 2 {
		t.Fatalf("expected 2 incorrect, got %d", result.Incorrect)
	}
}

func TestParsePolicyFallsBackSafely(t *testing.T) {
	if got := ParsePolicy("not json"); got.Version != 1 || got.Correct != 1 {
		t.Fatalf("corrupt policy should fall back to v1, got %+v", got)
	}
	if got := ParsePolicy(`{"version":2}`); got.Correct != 2 || got.Wrong != 1 {
		t.Fatalf("known policy version should fill defaults, got %+v", got)
	}
}

func TestPointsAreSeparateFromMarks(t *testing.T) {
	if PointsForCompletion("mock", 90) != 50 {
		t.Fatalf("mock with >=70%% should award 50 points")
	}
	if PointsForCompletion("practice", 0) != 10 {
		t.Fatalf("practice should award a base 10 points")
	}
}
