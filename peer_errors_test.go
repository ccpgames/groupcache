package groupcache

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	pb "github.com/ccpgames/groupcache/v3/groupcachepb"
)

// TestHTTPGetterStatusCodeToError verifies how httpGetter.Get maps a peer's HTTP
// status code onto a typed error. It guards two commits:
//   - 3c4c1a8 "don't retry or log error when peer is shutting down": 410 -> ErrPeerGone
//   - 7b5bdd6 "don't retry if group does not exist":                 503 -> ErrNoSuchGroup
//
// 404 -> ErrNotFound is included as a control to make sure the new mappings did
// not disturb the existing behaviour.
func TestHTTPGetterStatusCodeToError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		// matches reports whether err is the error type we expect.
		matches func(error) bool
		wantMsg string
	}{
		{
			name:       "410 Gone maps to ErrPeerGone",
			statusCode: http.StatusGone,
			body:       "draining",
			matches:    func(err error) bool { return errors.Is(err, &ErrPeerGone{}) },
			wantMsg:    "draining",
		},
		{
			name:       "503 Service Unavailable maps to ErrNoSuchGroup",
			statusCode: http.StatusServiceUnavailable,
			body:       "no such group: foo",
			matches:    func(err error) bool { return errors.Is(err, &ErrNoSuchGroup{}) },
			wantMsg:    "no such group: foo",
		},
		{
			name:       "404 Not Found still maps to ErrNotFound",
			statusCode: http.StatusNotFound,
			body:       "missing",
			matches:    func(err error) bool { return errors.Is(err, &ErrNotFound{}) },
			wantMsg:    "missing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, tc.body, tc.statusCode)
			}))
			defer ts.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			h := &httpGetter{
				peer:               ts.URL,
				baseURL:            ts.URL + defaultBasePath,
				peerLifetimeCtx:    ctx,
				peerLifetimeCancel: cancel,
			}

			group, key := "g", "k"
			err := h.Get(ctx, &pb.GetRequest{Group: &group, Key: &key}, &pb.GetResponse{})
			if err == nil {
				t.Fatalf("expected an error for status %d, got nil", tc.statusCode)
			}
			if !tc.matches(err) {
				t.Fatalf("error %T (%v) is not the expected type for status %d", err, err, tc.statusCode)
			}
			// http.Error appends a trailing newline which Get trims away.
			if err.Error() != tc.wantMsg {
				t.Fatalf("error message = %q, want %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

// stubGetter is a minimal ProtoGetter; callRemoteIfRemoteOwner only needs a
// non-nil owner whose GetURL() can be logged. The actual error under test is
// produced by the fn passed to callRemoteIfRemoteOwner.
type stubGetter struct{ url string }

func (s stubGetter) Get(context.Context, *pb.GetRequest, *pb.GetResponse) error { return nil }
func (s stubGetter) Remove(context.Context, *pb.GetRequest) error               { return nil }
func (s stubGetter) Set(context.Context, *pb.SetRequest) error                  { return nil }
func (s stubGetter) GetURL() string                                             { return s.url }

type singlePeerPicker struct{ peer ProtoGetter }

func (p singlePeerPicker) PickPeer(string) (ProtoGetter, bool) { return p.peer, true }
func (p singlePeerPicker) GetAll() []ProtoGetter               { return []ProtoGetter{p.peer} }

// TestCallRemoteIfRemoteOwnerRetryBehaviour verifies the retry decision made in
// callRemoteIfRemoteOwner for the different error types. ErrPeerGone (commit
// 3c4c1a8) and ErrNoSuchGroup (commit 7b5bdd6) must NOT be retried, since the
// peer will never produce a useful answer; ErrRemoteCall must still be retried.
func TestCallRemoteIfRemoteOwnerRetryBehaviour(t *testing.T) {
	// Swap in a logger that writes to t.Log so the error-level logs from the
	// retried/permanent paths don't go to stderr.
	oldLogger := logger
	t.Cleanup(func() { logger = oldLogger })
	logger = testLogger{tb: t}

	g := newGroup("retryBehaviourTest", 1<<20,
		GetterFunc(func(ctx context.Context, key string, dest Sink) error { return nil }),
		singlePeerPicker{peer: stubGetter{url: "http://peer:8080"}})
	t.Cleanup(func() { DeregisterGroup(g.name) })

	tests := []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{
			name:      "ErrPeerGone is not retried",
			err:       &ErrPeerGone{Msg: "shutting down"},
			wantRetry: false,
		},
		{
			name:      "ErrNoSuchGroup is not retried",
			err:       &ErrNoSuchGroup{Msg: "no such group"},
			wantRetry: false,
		},
		{
			name:      "ErrRemoteCall is retried",
			err:       &ErrRemoteCall{Msg: "transient"},
			wantRetry: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			remoteOwner, _, err := g.callRemoteIfRemoteOwner(context.Background(), "someKey",
				func(ctx context.Context, peer ProtoGetter) error {
					atomic.AddInt32(&calls, 1)
					return tc.err
				})

			if !remoteOwner {
				t.Fatalf("expected remoteOwner to be true")
			}
			if !errors.Is(err, tc.err) {
				t.Fatalf("expected returned error to wrap %v, got %v", tc.err, err)
			}

			got := atomic.LoadInt32(&calls)
			if tc.wantRetry {
				if got <= 1 {
					t.Fatalf("expected fn to be retried (called more than once), got %d call(s)", got)
				}
			} else {
				if got != 1 {
					t.Fatalf("expected fn to be called exactly once (no retry), got %d call(s)", got)
				}
			}
		})
	}
}
