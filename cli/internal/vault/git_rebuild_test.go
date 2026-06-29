package vault

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A real commit fires a POST to the configured rebuild endpoint, carrying the bearer token.
func TestCommitAndPushTriggersSiteRebuild(t *testing.T) {
	repo := initRepo(t)
	if err := writeFile(repo, "note.md", "hi"); err != nil {
		t.Fatal(err)
	}

	type capture struct{ method, auth string }
	got := make(chan capture, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- capture{r.Method, r.Header.Get("Authorization")}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	rb := &SiteRebuild{URL: srv.URL + "/rebuild", Token: "s3cret"}
	if err := CommitAndPush(repo, "add note", nil, rb, newTestLogger(t)); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	select {
	case c := <-got:
		if c.method != http.MethodPost {
			t.Errorf("method = %q, want POST", c.method)
		}
		if c.auth != "Bearer s3cret" {
			t.Errorf("auth = %q, want %q", c.auth, "Bearer s3cret")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rebuild endpoint was never called")
	}
}

// An unreachable rebuild endpoint must never fail the content job — the commit still succeeds.
func TestSiteRebuildUnreachableIsNonFatal(t *testing.T) {
	repo := initRepo(t)
	if err := writeFile(repo, "note.md", "hi"); err != nil {
		t.Fatal(err)
	}
	// Port 1 has nothing listening; the POST errors and must be swallowed.
	rb := &SiteRebuild{URL: "http://127.0.0.1:1/rebuild"}
	if err := CommitAndPush(repo, "add note", nil, rb, newTestLogger(t)); err != nil {
		t.Fatalf("unreachable site must be non-fatal, got %v", err)
	}
}

// No staged changes → no commit → no rebuild trigger (we don't rebuild on empty runs).
func TestSiteRebuildNotFiredWithoutCommit(t *testing.T) {
	repo := initRepo(t)
	called := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	rb := &SiteRebuild{URL: srv.URL}
	if err := CommitAndPush(repo, "noop", nil, rb, newTestLogger(t)); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}
	select {
	case <-called:
		t.Fatal("rebuild fired without a commit")
	case <-time.After(200 * time.Millisecond):
		// good — nothing was committed, so nothing was triggered
	}
}
