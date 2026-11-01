package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestVersePostSendsKeyAndEvent(t *testing.T) {
	var got verseEvent
	var key, path string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, path = r.Header.Get("X-Internal-Key"), r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"status":"posted"}`))
	}))
	defer ts.Close()
	v := &Verse{URL: ts.URL + "/", Key: "k-secret"}
	ev := verseEvent{EventID: "badge:a:b:r1", Account: "a", Category: "badge", Subject: "First Steps"}
	if err := v.post(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if key != "k-secret" || path != "/internal/v1/activity" || got != ev {
		t.Fatalf("sent %q %q %+v", key, path, got)
	}
}

func TestVersePostRetriesServerErrorsOnly(t *testing.T) {
	var n int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(503)
		}
	}))
	defer ts.Close()
	v := &Verse{URL: ts.URL, Key: "k", Backoff: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}}
	if err := v.post(context.Background(), verseEvent{}); err != nil || n != 3 {
		t.Fatalf("err %v after %d calls", err, n)
	}

	n = 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(401)
	}))
	defer bad.Close()
	v = &Verse{URL: bad.URL, Key: "wrong", Backoff: []time.Duration{time.Millisecond}}
	if err := v.post(context.Background(), verseEvent{}); err == nil || n != 1 {
		t.Fatalf("a rejected request must not be retried: err %v, %d calls", err, n)
	}
}

func TestBadgeEarnedIsOffWithoutVerse(t *testing.T) {
	(&Server{}).badgeEarned("a", "badge-x", "r1") // must not panic or touch the store
}
