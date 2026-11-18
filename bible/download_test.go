package bible

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// oneChapter is the smallest book document fetchBook accepts.
const oneChapter = `{"translation":"King James Version","abbreviation":"kjv",
	"chapters":[{"chapter":1,"verses":[{"chapter":1,"verse":1,"text":"In the beginning"}]}]}`

// stubGetBible points the downloader at a local server and removes the retry
// waits, restoring both when the test ends. The handler receives the 1-based
// number of the request it is answering.
func stubGetBible(t *testing.T, handler func(w http.ResponseWriter, attempt int32)) *atomic.Int32 {
	t.Helper()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, calls.Add(1))
	}))
	t.Cleanup(srv.Close)

	oldURL, oldBackoff := getBibleBaseURL, fetchBackoff
	getBibleBaseURL = srv.URL
	fetchBackoff = []time.Duration{0, 0}
	t.Cleanup(func() { getBibleBaseURL, fetchBackoff = oldURL, oldBackoff })

	return &calls
}

// TestFetchBookRetriesTransientFailures: one bad response out of 66 requests
// must not cost the whole download. Each kind of blip is tried: a 5xx, a rate
// limit, and a body cut off mid-document.
func TestFetchBookRetriesTransientFailures(t *testing.T) {
	calls := stubGetBible(t, func(w http.ResponseWriter, attempt int32) {
		switch attempt {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
		case 2:
			_, _ = w.Write([]byte(`{"chapters":[{"chapter":1,"ver`)) // truncated JSON
		default:
			_, _ = w.Write([]byte(oneChapter))
		}
	})

	doc, err := fetchBookWithRetry(http.DefaultClient, "kjv", 1)
	if err != nil {
		t.Fatalf("fetchBookWithRetry() error after two transient failures: %v", err)
	}
	if len(doc.Chapters) != 1 || len(doc.Chapters[0].Verses) != 1 {
		t.Errorf("fetched document = %+v, want one chapter of one verse", doc)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("made %d requests, want 3 (two failures and the success)", got)
	}
}

// TestFetchBookGivesUp: the retries are bounded, and the error that comes back
// is a real one rather than a nil document.
func TestFetchBookGivesUp(t *testing.T) {
	calls := stubGetBible(t, func(w http.ResponseWriter, attempt int32) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	if _, err := fetchBookWithRetry(http.DefaultClient, "kjv", 1); err == nil {
		t.Fatal("fetchBookWithRetry() reported success against a server that only fails")
	}
	if got, want := calls.Load(), int32(1+len(fetchBackoff)); got != want {
		t.Errorf("made %d requests, want %d (one attempt plus one per backoff step)", got, want)
	}
}

// TestFetchBookDoesNotRetryAPermanentFailure: a 404 is the API saying it has
// no such translation. Asking twice more only delays telling the user.
func TestFetchBookDoesNotRetryAPermanentFailure(t *testing.T) {
	calls := stubGetBible(t, func(w http.ResponseWriter, attempt int32) {
		w.WriteHeader(http.StatusNotFound)
	})

	if _, err := fetchBookWithRetry(http.DefaultClient, "nope", 1); err == nil {
		t.Fatal("fetchBookWithRetry() accepted a 404")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d requests for a 404, want 1", got)
	}
}

// TestFetchBookRetriesARateLimit: 429 is a 4xx that does mean "try again".
func TestFetchBookRetriesARateLimit(t *testing.T) {
	calls := stubGetBible(t, func(w http.ResponseWriter, attempt int32) {
		if attempt == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(oneChapter))
	})

	if _, err := fetchBookWithRetry(http.DefaultClient, "kjv", 1); err != nil {
		t.Fatalf("fetchBookWithRetry() error after a 429: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("made %d requests, want 2", got)
	}
}
