// Package domain holds pure business rules with no I/O so they can be tested exhaustively.
package domain

import "encoding/json"

// ScoringPolicy is versioned: every finalized attempt records the version that produced its score,
// which is what makes historical scores reproducible years later.
type ScoringPolicy struct {
	Version         int  `json:"version"`
	Correct         int  `json:"correct"`
	Wrong           int  `json:"wrong"`
	Unanswered      int  `json:"unanswered"`
	NegativeMarking bool `json:"negativeMarking"`
}

// PolicyV1 is the launch policy: one mark per correct answer, no negative marking.
var PolicyV1 = ScoringPolicy{Version: 1, Correct: 1, Wrong: 0, Unanswered: 0}

// PolicyV2 demonstrates versioned behaviour changes without rewriting history.
var PolicyV2 = ScoringPolicy{Version: 2, Correct: 2, Wrong: 1, Unanswered: 0, NegativeMarking: true}

var policies = map[int]ScoringPolicy{1: PolicyV1, 2: PolicyV2}

// ParsePolicy decodes a stored policy, falling back to V1 when the payload is absent or corrupt.
func ParsePolicy(raw string) ScoringPolicy {
	if raw == "" {
		return PolicyV1
	}
	var p ScoringPolicy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return PolicyV1
	}
	if base, ok := policies[p.Version]; ok {
		if p.Correct == 0 {
			p.Correct = base.Correct
		}
		if p.Wrong == 0 {
			p.Wrong = base.Wrong
		}
	}
	return p
}

// SnapQuestion is the immutable per-attempt copy of a question. Answer keys live here (and only
// here), so a later correction to the source question cannot rewrite a completed attempt.
type SnapQuestion struct {
	QvID          string     `json:"qvId"`
	QuestionID    string     `json:"questionId"`
	SubjectID     string     `json:"subjectId,omitempty"`
	SubjectName   string     `json:"subjectName,omitempty"`
	TopicID       string     `json:"topicId,omitempty"`
	TopicName     string     `json:"topicName,omitempty"`
	Stem          string     `json:"stem"`
	Options       []Option   `json:"options"`
	AnswerOption  string     `json:"answerOptionId"`
	Explanation   string     `json:"explanation,omitempty"`
	Difficulty    string     `json:"difficulty,omitempty"`
	Points        int        `json:"points"`
	Position      int        `json:"position"`
}

// Option is a single answer choice in presentation order.
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// SnapResponse is the learner's current answer for one question.
type SnapResponse struct {
	QvID             string
	SelectedOptionID *string
}

// QuestionResult is the per-question outcome produced by scoring.
type QuestionResult struct {
	QvID             string  `json:"qvId"`
	SelectedOptionID *string `json:"selectedOptionId"`
	AnswerOptionID   string  `json:"answerOptionId"`
	IsCorrect        *bool   `json:"isCorrect"`
	Awarded          int     `json:"awarded"`
}

// Breakdown is the deterministic result of scoring one attempt.
type Breakdown struct {
	Raw           int              `json:"raw"`
	Max           int              `json:"max"`
	Percent       int              `json:"percent"`
	Correct       int              `json:"correct"`
	Incorrect     int              `json:"incorrect"`
	Unanswered    int              `json:"unanswered"`
	PolicyVersion int              `json:"policyVersion"`
	PerQuestion   []QuestionResult `json:"perQuestion"`
}

// ScoreAttempt computes the attempt score. It is a pure function of (snapshot, responses, policy),
// so the same inputs always produce the same output — including after a restart or a replay.
func ScoreAttempt(questions []SnapQuestion, responses []SnapResponse, policy ScoringPolicy) Breakdown {
	byQv := make(map[string]SnapResponse, len(responses))
	for _, r := range responses {
		byQv[r.QvID] = r
	}

	out := Breakdown{PolicyVersion: policy.Version, PerQuestion: make([]QuestionResult, 0, len(questions))}
	for _, q := range questions {
		points := q.Points
		if points <= 0 {
			points = 1
		}
		correctMark := policy.Correct
		if correctMark <= 0 {
			correctMark = 1
		}
		out.Max += points * correctMark

		result := QuestionResult{QvID: q.QvID, AnswerOptionID: q.AnswerOption}
		selected := byQv[q.QvID].SelectedOptionID
		if selected == nil || *selected == "" {
			out.Unanswered++
			result.Awarded = policy.Unanswered * points
			out.Raw += result.Awarded
			out.PerQuestion = append(out.PerQuestion, result)
			continue
		}

		result.SelectedOptionID = selected
		isCorrect := *selected == q.AnswerOption
		result.IsCorrect = &isCorrect
		if isCorrect {
			out.Correct++
			result.Awarded = correctMark * points
		} else {
			out.Incorrect++
			if policy.NegativeMarking {
				result.Awarded = -policy.Wrong * points
			}
		}
		out.Raw += result.Awarded
		out.PerQuestion = append(out.PerQuestion, result)
	}

	if out.Max > 0 {
		out.Percent = out.Raw * 100 / out.Max
		if out.Percent < 0 {
			out.Percent = 0
		}
	}
	if out.Raw < 0 {
		out.Raw = 0
	}
	return out
}

// PointsForCompletion awards engagement points, which are a separate asset from marks and money.
func PointsForCompletion(mode string, percent int) int {
	base := map[string]int{"practice": 10, "mock": 40, "class": 25}[mode]
	if base == 0 {
		base = 10
	}
	if percent >= 70 {
		base += 10
	}
	return base
}
