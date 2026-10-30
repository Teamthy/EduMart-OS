// Command loadtest drives a realistic learner workload against a running API and reports what it measured.
//
// It exists because "no load tests" was a named gap in the readiness document, and because a performance
// claim nobody can reproduce is not a measurement. Every number this prints comes with the concurrency and
// duration that produced it, and the tool exits non-zero when a stated budget is missed — so it can be a
// gate rather than a dashboard.
//
// On pacing: the API rate-limits assessment traffic to 120 requests a minute per client address, and one
// journey is seven of those requests. A virtual user therefore cannot exceed one journey every 3.5 seconds
// without being throttled — so that is the default think time. Pacing beneath the limit keeps the run
// measuring the server; hammering through it would measure the limiter instead, and a report that called the
// resulting 429s "server errors" would be wrong twice over.
//
// Two things it does deliberately:
//
//   - It exercises the **whole attempt lifecycle**, not one endpoint. A benchmark of GET /products would
//     measure the cheapest thing in the system and say nothing about the write path, which is where the
//     server owns deadlines, optimistic versions and duplicate-submission protection.
//   - It reports **percentiles, not averages**. A mean of 30ms hides the second request that took 900ms, and
//     the timeouts are the only number a user notices.
//
// Usage:
//
//	go run ./cmd/loadtest -base http://localhost:8080 -concurrency 32 -duration 60s
//	go run ./cmd/loadtest -base http://localhost:8080 -concurrency 64 -duration 90s -max-p95 250ms -report report.json
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Credentials the seeded demo users share. The tool logs in as them rather than registering, because
// registration hashes a password with Argon2 — deliberately expensive — and a load generator that spends its
// CPU on password hashing measures the tool, not the server.
const defaultLearners = 40

func main() {
	var (
		base        = flag.String("base", "http://localhost:8080", "API base URL")
		concurrency = flag.Int("concurrency", 32, "concurrent virtual users")
		duration    = flag.Duration("duration", 45*time.Second, "how long to run the steady-state phase")
		ramp        = flag.Duration("ramp", 5*time.Second, "spread virtual user start over this period")
		warmup      = flag.Duration("warmup", 3*time.Second, "discarded phase before measuring")
		maxP95      = flag.Duration("max-p95", 0, "fail if the overall p95 exceeds this (0 = no budget)")
		maxP99      = flag.Duration("max-p99", 0, "fail if the overall p99 exceeds this (0 = no budget)")
		maxError    = flag.Float64("max-error-rate", 0, "fail if the error rate exceeds this fraction (0 = no budget)")
		report      = flag.String("report", "", "write the JSON report here")
		readShare   = flag.Int("read-share", 30, "percent of journeys that are read-only (browsing)")
		accounts    = flag.Int("accounts", 8, "distinct learner accounts to spread virtual users across")
		stateFile   = flag.String("state", "", "where to cache the provisioned accounts (default: temp dir)")
		seed        = flag.Int64("seed", 1, "PRNG seed, so a run can be repeated")
		think       = flag.Duration("think", 3500*time.Millisecond, "pause between journeys; see the note on pacing in the package comment")
	)
	flag.Parse()

	started := time.Now().UTC()
	baseURL := strings.TrimRight(*base, "/")

	if err := waitReady(baseURL, 30*time.Second); err != nil {
		fatalf("the API is not ready: %v", err)
	}

	statePath := *stateFile
	if statePath == "" {
		statePath = filepath.Join(os.TempDir(), "edumart-loadtest-accounts.json")
	}
	pool, err := provisionAccounts(baseURL, *accounts, statePath)
	if err != nil {
		fatalf("provision accounts: %v", err)
	}
	users, err := buildUsers(baseURL, pool, *concurrency)
	if err != nil {
		fatalf("%v", err)
	}
	subjects, err := discoverSubjects(baseURL)
	if err != nil {
		fatalf("discover subjects: %v", err)
	}

	run := &run{
		base:     baseURL,
		users:    users,
		metrics:  newCollector(),
		seed:     *seed,
		readPct:  *readShare,
		think:    *think,
		subjects: subjects,
	}

	// Warmup: connections, prepared statements, the seeded catalogue falling into the database's cache. Its
	// samples are discarded, because otherwise the report describes a cold server and calls it steady state.
	if *warmup > 0 {
		fmt.Fprintf(os.Stderr, "warmup: %d users for %s\n", *concurrency, *warmup)
		run.phase(*concurrency, *warmup, *ramp)
		run.metrics = newCollector()
	}

	fmt.Fprintf(os.Stderr, "measuring: %d users for %s (ramp %s)\n", *concurrency, *duration, *ramp)
	run.phase(*concurrency, *duration, *ramp)

	summary := run.metrics.summarise(*concurrency, *duration)
	summary.Started = started.Format(time.RFC3339)
	summary.Engine = detectEngine(baseURL)
	summary.GOMAXPROCS = runtime.NumCPU()

	printReport(summary)

	if *report != "" {
		payload, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			fatalf("encode report: %v", err)
		}
		if err := os.WriteFile(*report, append(payload, '\n'), 0o644); err != nil {
			fatalf("write report: %v", err)
		}
		fmt.Fprintf(os.Stderr, "report written to %s\n", *report)
	}

	// Budgets are checked last, after the numbers have been printed: a failing gate is far more useful when
	// the reader can see what failed.
	failures := []string{}
	if *maxError > 0 && summary.ErrorRate > *maxError {
		failures = append(failures, fmt.Sprintf("error rate %.3f%% exceeds the budget of %.3f%%",
			summary.ErrorRate*100, *maxError*100))
	}
	if *maxP95 > 0 && time.Duration(summary.P95MS*float64(time.Millisecond)) > *maxP95 {
		failures = append(failures, fmt.Sprintf("p95 %s exceeds the budget of %s",
			time.Duration(summary.P95MS*float64(time.Millisecond)), *maxP95))
	}
	if *maxP99 > 0 && time.Duration(summary.P99MS*float64(time.Millisecond)) > *maxP99 {
		failures = append(failures, fmt.Sprintf("p99 %s exceeds the budget of %s",
			time.Duration(summary.P99MS*float64(time.Millisecond)), *maxP99))
	}
	// Zero successful journeys means the run measured nothing. That must never look like a pass.
	if summary.Journeys == 0 {
		failures = append(failures, "no journey completed, so this run measured nothing")
	}
	for _, failure := range failures {
		fmt.Fprintf(os.Stderr, "FAIL: %s\n", failure)
	}
	if len(failures) > 0 {
		os.Exit(1)
	}
	if summary.Throttled > 0 {
		fmt.Fprintf(os.Stderr, "budgets met, but %.1f%% of traffic was rate-limited: the run is paced too "+
			"fast to describe the server's capacity\n", 100*float64(summary.Throttled)/float64(summary.Requests))
		return
	}
	fmt.Fprintln(os.Stderr, "budgets met")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// waitReady blocks until /readyz answers, so a load run never measures a process that is still migrating.
func waitReady(base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var last error
	for time.Now().Before(deadline) {
		res, err := client.Get(base + "/readyz")
		if err == nil {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("/readyz answered %d", res.StatusCode)
		} else {
			last = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("timed out")
	}
	return last
}

// detectEngine asks the API what it is running on, so the report cannot be read as if SQLite and PostgreSQL
// were the same experiment.
func detectEngine(base string) string {
	res, err := http.Get(base + "/healthz")
	if err != nil {
		return "unknown"
	}
	defer res.Body.Close()
	var body struct {
		Driver string `json:"driver"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Driver == "" {
		return "unknown"
	}
	return body.Driver
}

// ─────────────────────────────── virtual users ───────────────────────────────

type user struct {
	email  string
	token  string
	client *http.Client
	// clientIP is a synthetic address sent as X-Forwarded-For. Every virtual user on one machine shares
	// 127.0.0.1, and the API keys its rate limits on the client address — so without this the run measures
	// one client's budget rather than a cohort of learners. The API honours the header only when
	// EDUMART_TRUST_PROXY=true, which is exactly the deployment shape (a proxy forwarding the real address)
	// this is meant to imitate. Pretending the limiter does not exist would measure a server nobody runs.
	clientIP string
}

// session is one attempt, from start to submit.
type session struct {
	id        string
	questions []string // qvIds, in the order the server returned them
	version   int
}

type run struct {
	base    string
	users   []user
	metrics *collector
	seed    int64
	readPct int
	think   time.Duration
	// subjects are the ids a practice attempt can be started against. A real client reads the catalogue;
	// a load generator that hard-codes a seed id breaks the moment the seed changes.
	subjects []string
}

// accountPool provisions the accounts a load run reuses, and caches their credentials.
//
// This exists because the API rate-limits sign-in and registration to 30 attempts a minute — correctly, and
// a load generator that ignored that would be measuring a server nobody deploys. The pool is therefore small,
// created once, and reused: virtual users are spread across accounts instead of each getting one, which is
// also closer to a real cohort sharing a few institutional licences.
//
// The password is a fixture, not a credential: the accounts exist only in a test database and are named
// `loadtest+…@edumart.test`.
type account struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

const accountPassword = "LoadTest123!"

func provisionAccounts(base string, count int, statePath string) ([]account, error) {
	accounts := loadAccounts(statePath)
	for len(accounts) < count {
		email := fmt.Sprintf("loadtest+%d@edumart.test", len(accounts))
		if err := ensureAccount(base, email); err != nil {
			return nil, fmt.Errorf("provision %s: %w", email, err)
		}
		accounts = append(accounts, account{Email: email, Password: accountPassword})
	}
	if err := saveAccounts(statePath, accounts); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not cache accounts: %v\n", err)
	}
	return accounts, nil
}

func loadAccounts(path string) []account {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var accounts []account
	if err := json.Unmarshal(raw, &accounts); err != nil {
		return nil
	}
	return accounts
}

func saveAccounts(path string, accounts []account) error {
	if path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// ensureAccount makes one account exist, waiting out the auth rate limit rather than failing the run.
func ensureAccount(base, email string) error {
	deadline := time.Now().Add(3 * time.Minute)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		status, _, err := call(base, http.DefaultClient, "POST", "/v1/auth/register", "", map[string]any{
			"displayName": "Load Test", "email": email, "password": accountPassword, "client": "web",
		})
		if err != nil {
			return err
		}
		switch status {
		case http.StatusCreated, http.StatusOK:
			return nil
		case http.StatusConflict:
			return nil // already provisioned by an earlier run
		case http.StatusTooManyRequests:
			// The limiter is doing its job. Waiting is the correct client behaviour, and doing it here keeps
			// the rate limit in the product rather than switching it off for the benchmark.
			time.Sleep(time.Duration(2+attempt) * time.Second)
			continue
		default:
			return fmt.Errorf("register answered %d", status)
		}
	}
	return fmt.Errorf("auth rate limit never let the account be created")
}

// tokensFor signs in one account, retrying through the rate limiter.
func tokensFor(base, email, password string) (string, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		status, payload, err := call(base, http.DefaultClient, "POST", "/v1/auth/login", "",
			map[string]any{"email": email, "password": password})
		if err != nil {
			return "", err
		}
		if status == http.StatusOK {
			token := tokenFrom(payload)
			if token == "" {
				return "", fmt.Errorf("login answered 200 without a token")
			}
			return token, nil
		}
		if status == http.StatusTooManyRequests {
			time.Sleep(time.Duration(2+attempt) * time.Second)
			continue
		}
		return "", fmt.Errorf("login answered %d", status)
	}
	return "", fmt.Errorf("auth rate limit never let the account sign in")
}

// buildUsers spreads the requested concurrency across the provisioned accounts.
func buildUsers(base string, accounts []account, count int) ([]user, error) {
	users := make([]user, 0, count)
	for i := 0; i < count; i++ {
		account := accounts[i%len(accounts)]
		token, err := tokensFor(base, account.Email, account.Password)
		if err != nil {
			return nil, fmt.Errorf("sign in as %s: %w", account.Email, err)
		}
		users = append(users, user{
			email:    account.Email,
			token:    token,
			clientIP: fmt.Sprintf("198.51.100.%d", (i%250)+1),
			// One transport per user with keep-alives on: the connection pool a real client would hold.
			client: &http.Client{
				Timeout: 20 * time.Second,
				Transport: &http.Transport{
					MaxIdleConnsPerHost: 4,
					MaxConnsPerHost:     4,
					IdleConnTimeout:     90 * time.Second,
				},
			},
		})
	}
	return users, nil
}

// discoverSubjects reads the public catalogue, which is what a client does before it can start anything.
func discoverSubjects(base string) ([]string, error) {
	status, payload, err := call(base, http.DefaultClient, "GET", "/v1/subjects", "", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("/v1/subjects answered %d", status)
	}
	raw, _ := payload["subjects"].([]any)
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		if subject, ok := item.(map[string]any); ok {
			if id, ok := subject["id"].(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("the catalogue is empty, so an attempt cannot be started")
	}
	return ids, nil
}

func login(base, email, password string) (string, error) {
	status, payload, err := call(base, http.DefaultClient, "POST", "/v1/auth/login", "",
		map[string]any{"email": email, "password": password})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("login answered %d", status)
	}
	return tokenFrom(payload), nil
}

func tokenFrom(payload map[string]any) string {
	// The session token is called `token` on the wire; the camelCase names are accepted too so the tool keeps
	// working if the login response is ever reshaped.
	for _, key := range []string{"token", "accessToken"} {
		if token, ok := payload[key].(string); ok && token != "" {
			return token
		}
	}
	if session, ok := payload["session"].(map[string]any); ok {
		if token, ok := session["accessToken"].(string); ok {
			return token
		}
	}
	return ""
}

// phase runs virtual users until the deadline.
func (r *run) phase(users int, duration, ramp time.Duration) {
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var journeys int64

	for i := 0; i < users; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			// Ramp: starting 64 users in the same microsecond measures a thundering herd, which is a
			// different (and also worth knowing) experiment.
			if ramp > 0 && users > 1 {
				time.Sleep(time.Duration(rand.Int63n(int64(ramp))))
			}
			u := &r.users[index%len(r.users)]
			random := rand.New(rand.NewSource(r.seed + int64(index)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				r.metrics.begin()
				if r.think > 0 {
					// A person does not submit twenty attempts a second. Without this the run measures the
					// rate limiter's tolerance rather than the server's capacity — and the limiter, not the
					// database, would be the only thing the numbers described.
					jitter := time.Duration(random.Int63n(int64(r.think) + 1))
					time.Sleep(r.think + jitter)
				}
				if err := r.journey(u, random); err != nil {
					// Individual requests are recorded where they happen; a journey-level failure is a
					// correctness problem (the API contradicted itself mid-flow) rather than a latency one.
					r.metrics.journeyFailed(err)
				} else {
					// Only a flow that reached its last step counts. A journey that died on the first
					// request is not a journey, and counting it would make a broken server look busy.
					atomic.AddInt64(&journeys, 1)
				}
			}
		}(i)
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()
	r.metrics.setJourneys(journeys)
}

// journey is what a learner actually does: look at their home, start an attempt, answer it, submit it.
//
// It returns an error only when the flow should have completed and did not — a 429, a 5xx, a session with no
// id. A correct refusal (a stale-version 409 on a write, which is the server defending an invariant) is part
// of the flow, not a failure of it.
func (r *run) journey(u *user, random *rand.Rand) error {
	if random.Intn(100) < r.readPct {
		// The browsing path: two reads, no writes. Mixed in because a benchmark of only the write path
		// overstates how often the write path is hit in production.
		if status, _, err := r.request(u, "browse", "GET", "/v1/products", nil); err != nil || status != http.StatusOK {
			return fmt.Errorf("browse answered %d", status)
		}
		if status, _, err := r.request(u, "dashboard", "GET", "/v1/dashboard", nil); err != nil || status != http.StatusOK {
			return fmt.Errorf("dashboard answered %d", status)
		}
		return nil
	}

	if len(r.subjects) == 0 {
		return fmt.Errorf("no subjects to practise")
	}
	subject := r.subjects[random.Intn(len(r.subjects))]
	status, payload, err := r.request(u, "start-attempt", "POST", "/v1/practice-sessions",
		map[string]any{"subjectId": subject, "count": 3})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("start-attempt answered %d", status)
	}
	started, _ := payload["session"].(map[string]any)
	if started == nil {
		return fmt.Errorf("start returned no session")
	}
	session := &session{id: fmt.Sprint(started["session_id"])}
	questions, _ := started["questions"].([]any)
	if len(questions) == 0 {
		return fmt.Errorf("a started attempt had no questions")
	}
	for _, raw := range questions {
		question, _ := raw.(map[string]any)
		if question == nil {
			continue
		}
		qvID := fmt.Sprint(question["qvId"])
		session.questions = append(session.questions, qvID)
		// Answer with the first option: the tool measures the write path, not whether the answers are right.
		optionID := ""
		if options, ok := question["options"].([]any); ok && len(options) > 0 {
			if first, ok := options[0].(map[string]any); ok {
				optionID = fmt.Sprint(first["id"])
			}
		}
		if optionID == "" {
			continue
		}
		path := fmt.Sprintf("/v1/assessment-sessions/%s/responses/%s", session.id, qvID)
		status, saved, err := r.request(u, "save-answer", "PUT", path, map[string]any{
			"selectedOptionId": optionID,
			"expectedVersion":  session.version,
			"clientSequence":   session.version + 1,
		})
		if err != nil {
			return err
		}
		switch status {
		case http.StatusOK:
			if version, ok := saved["version"].(float64); ok {
				session.version = int(version)
			}
		case http.StatusConflict:
			// A stale write lost the race. That is the server doing its job; refresh and carry on rather
			// than treating a correct 409 as an error.
			if errorBody, ok := saved["error"].(map[string]any); ok {
				if details, ok := errorBody["details"].(map[string]any); ok {
					if current, ok := details["current_version"].(float64); ok {
						session.version = int(current)
					}
				}
			}
		default:
			return fmt.Errorf("save-answer answered %d", status)
		}
	}

	submitPath := fmt.Sprintf("/v1/assessment-sessions/%s/submit", session.id)
	status, _, err = r.request(u, "submit", "POST", submitPath, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("submit answered %d", status)
	}
	readPath := "/v1/assessment-sessions/" + session.id
	if status, _, err := r.request(u, "read-attempt", "GET", readPath, nil); err != nil || status != http.StatusOK {
		return fmt.Errorf("read-attempt answered %d", status)
	}
	return nil
}

// request performs one HTTP call, records its latency and status, and never returns a transport error to the
// caller's control flow: an unreachable server during a load test is data, not a panic.
func (r *run) request(u *user, name, method, path string, body any) (int, map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	started := time.Now()
	req, err := http.NewRequestWithContext(context.Background(), method, r.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+u.token)
	if u.clientIP != "" {
		req.Header.Set("X-Forwarded-For", u.clientIP)
	}
	res, err := u.client.Do(req)
	elapsed := time.Since(started)
	if err != nil {
		r.metrics.record(name, elapsed, 0, err)
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	payload := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	r.metrics.record(name, elapsed, res.StatusCode, nil)
	return res.StatusCode, payload, nil
}

// ─────────────────────────────── measurement ───────────────────────────────

type sample struct {
	duration time.Duration
	status   int
	err      error
}

type collector struct {
	mu        sync.Mutex
	samples   map[string][]sample
	journeyE  []string
	journeys  int64
	began     int64
	throttled int64
	// inFlight tracks concurrency actually achieved, which is not the same as the requested number when
	// the server is slow: a fixed 32 goroutines can still have only 3 requests open.
	peakInFlight int64
	current      int64
}

func newCollector() *collector {
	return &collector{samples: map[string][]sample{}}
}

func (c *collector) begin() {
	c.mu.Lock()
	c.began++
	c.mu.Unlock()
	n := atomic.AddInt64(&c.current, 1)
	for {
		peak := atomic.LoadInt64(&c.peakInFlight)
		if n <= peak || atomic.CompareAndSwapInt64(&c.peakInFlight, peak, n) {
			break
		}
	}
	atomic.AddInt64(&c.current, -1)
}

func (c *collector) record(name string, elapsed time.Duration, status int, err error) {
	c.mu.Lock()
	c.samples[name] = append(c.samples[name], sample{duration: elapsed, status: status, err: err})
	if status == http.StatusTooManyRequests {
		// Counted separately from errors: a 429 is the server protecting itself, not a failure. It still has
		// to be visible, because a run that spent a third of its traffic on refusals did not measure what it
		// claims to.
		c.throttled++
	}
	c.mu.Unlock()
}

func (c *collector) journeyFailed(err error) {
	c.mu.Lock()
	if len(c.journeyE) < 20 {
		c.journeyE = append(c.journeyE, err.Error())
	}
	c.mu.Unlock()
}

func (c *collector) setJourneys(n int64) { c.journeys = n }

type EndpointReport struct {
	Name       string      `json:"name"`
	Count      int         `json:"count"`
	Errors     int         `json:"errors"`
	ErrorRate  float64     `json:"errorRate"`
	P50MS      float64     `json:"p50Ms"`
	P95MS      float64     `json:"p95Ms"`
	P99MS      float64     `json:"p99Ms"`
	MaxMS      float64     `json:"maxMs"`
	RPS        float64     `json:"rps"`
	StatusFreq map[int]int `json:"statusCounts"`
}

type Summary struct {
	Started           string  `json:"started"`
	Engine            string  `json:"engine"`
	GOMAXPROCS        int     `json:"goMaxProcs"`
	Concurrency       int     `json:"concurrency"`
	DurationS         float64 `json:"durationSeconds"`
	Requests          int     `json:"requests"`
	Errors            int     `json:"errors"`
	ErrorRate         float64 `json:"errorRate"`
	RPS               float64 `json:"requestsPerSecond"`
	Journeys          int64   `json:"journeys"`
	JourneysPerSecond float64 `json:"journeysPerSecond"`
	// Throttled is how many requests a rate limit refused. Reported separately from errors, because the
	// difference between "the server is broken" and "we asked too fast" is the point of a load test.
	Throttled int64 `json:"throttled"`

	P50MS float64 `json:"p50Ms"`
	P95MS float64 `json:"p95Ms"`
	P99MS float64 `json:"p99Ms"`
	MaxMS float64 `json:"maxMs"`

	Endpoints []EndpointReport `json:"endpoints"`
	// JourneyErrors are the flows where the API contradicted itself (a session that started but had no id,
	// say). They are separate from HTTP errors because they mean the *protocol* broke under load.
	JourneyErrors []string `json:"journeyErrors"`
}

func (c *collector) summarise(concurrency int, duration time.Duration) Summary {
	summary := Summary{
		Concurrency:   concurrency,
		DurationS:     duration.Seconds(),
		Journeys:      c.journeys,
		Throttled:     c.throttled,
		JourneyErrors: c.journeyE,
	}
	if summary.JourneyErrors == nil {
		summary.JourneyErrors = []string{}
	}
	if duration > 0 {
		summary.JourneysPerSecond = float64(c.journeys) / duration.Seconds()
	}

	all := []time.Duration{}
	for name, samples := range c.samples {
		report := EndpointReport{Name: name, Count: len(samples), StatusFreq: map[int]int{}}
		durations := make([]time.Duration, 0, len(samples))
		for _, s := range samples {
			if s.err != nil {
				report.Errors++
				report.StatusFreq[0]++
				continue
			}
			if s.status >= 500 || s.status == http.StatusTooManyRequests {
				report.Errors++
			}
			report.StatusFreq[s.status]++
			durations = append(durations, s.duration)
			all = append(all, s.duration)
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		report.P50MS = percentileMS(durations, 0.50)
		report.P95MS = percentileMS(durations, 0.95)
		report.P99MS = percentileMS(durations, 0.99)
		if len(durations) > 0 {
			report.MaxMS = float64(durations[len(durations)-1].Microseconds()) / 1000
		}
		if duration > 0 {
			report.RPS = float64(len(samples)) / duration.Seconds()
		}
		if report.Count > 0 {
			report.ErrorRate = float64(report.Errors) / float64(report.Count)
		}
		summary.Requests += report.Count
		summary.Errors += report.Errors
		summary.Endpoints = append(summary.Endpoints, report)
	}
	sort.Slice(summary.Endpoints, func(i, j int) bool { return summary.Endpoints[i].Name < summary.Endpoints[j].Name })

	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	summary.P50MS = percentileMS(all, 0.50)
	summary.P95MS = percentileMS(all, 0.95)
	summary.P99MS = percentileMS(all, 0.99)
	if len(all) > 0 {
		summary.MaxMS = float64(all[len(all)-1].Microseconds()) / 1000
	}
	if summary.Requests > 0 {
		summary.ErrorRate = float64(summary.Errors) / float64(summary.Requests)
	}
	if duration > 0 {
		summary.RPS = float64(summary.Requests) / duration.Seconds()
	}
	return summary
}

// percentileMS uses the nearest-rank method: the value below which that fraction of samples fall. With small
// samples this is exact and explainable, which an interpolated percentile is not.
func percentileMS(sorted []time.Duration, fraction float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(fraction*float64(len(sorted))+0.999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return float64(sorted[index].Microseconds()) / 1000
}

func printReport(s Summary) {
	fmt.Printf("\nrun: %d users for %.0fs on %s (%d Go CPUs)\n", s.Concurrency, s.DurationS, s.Engine, s.GOMAXPROCS)
	fmt.Printf("%d requests, %.0f req/s, %d journeys (%.1f/s), error rate %.2f%%",
		s.Requests, s.RPS, s.Journeys, s.JourneysPerSecond, s.ErrorRate*100)
	if s.Throttled > 0 {
		fmt.Printf(", throttled %d (%.1f%% of traffic)",
			s.Throttled, 100*float64(s.Throttled)/float64(s.Requests))
	}
	fmt.Println()
	fmt.Printf("latency  p50 %.1fms  p95 %.1fms  p99 %.1fms  max %.1fms\n",
		s.P50MS, s.P95MS, s.P99MS, s.MaxMS)
	fmt.Printf("\n%-14s %8s %8s %9s %9s %9s %9s %8s\n",
		"endpoint", "count", "errors", "p50", "p95", "p99", "max", "req/s")
	for _, e := range s.Endpoints {
		fmt.Printf("%-14s %8d %8d %8.1fms %8.1fms %8.1fms %8.1fms %8.1f\n",
			e.Name, e.Count, e.Errors, e.P50MS, e.P95MS, e.P99MS, e.MaxMS, e.RPS)
	}
	if len(s.JourneyErrors) > 0 {
		fmt.Printf("\njourney errors (%d):\n", len(s.JourneyErrors))
		for _, message := range s.JourneyErrors {
			fmt.Printf("  - %s\n", message)
		}
	}
	fmt.Println()
}

// call is the one-shot JSON helper used before the measured phase (login, registration). It is deliberately
// separate from run.request: setup traffic must not land in the report's latency numbers.
func call(base string, client *http.Client, method, path, token string, body any) (int, map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	payload := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	return res.StatusCode, payload, nil
}
