package groupcache

// ErrNotFound should be returned from an implementation of `GetterFunc` to indicate the
// requested value is not available. When remote HTTP calls are made to retrieve values from
// other groupcache instances, returning this error will indicate to groupcache that the
// value requested is not available, and it should NOT attempt to call `GetterFunc` locally.
type ErrNotFound struct {
	Msg string
}

func (e *ErrNotFound) Error() string {
	if e.Msg == "" {
		return "not found error"
	}
	return e.Msg
}

func (e *ErrNotFound) Is(target error) bool {
	_, ok := target.(*ErrNotFound)
	return ok
}

// ErrRemoteCall is returned from `group.Get()` when a remote GetterFunc returns an
// error. When this happens `group.Get()` does not attempt to retrieve the value
// via our local GetterFunc.
type ErrRemoteCall struct {
	Msg string
}

func (e *ErrRemoteCall) Error() string {
	if e.Msg == "" {
		return "remote call error"
	}
	return e.Msg
}

func (e *ErrRemoteCall) Is(target error) bool {
	_, ok := target.(*ErrRemoteCall)
	return ok
}

// ErrPeerGone is returned when the peer we are trying to talk to is shutting down and is currently draining its queue.
// When receiving this error it doesn't make sense to retry as the peer will never respond with the actual answer.
type ErrPeerGone struct {
	Msg string
}

func (e *ErrPeerGone) Error() string {
	if e.Msg == "" {
		return "peer gone error"
	}
	return e.Msg
}

func (e *ErrPeerGone) Is(target error) bool {
	_, ok := target.(*ErrPeerGone)
	return ok
}
