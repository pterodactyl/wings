package transfer

import (
	"context"
	"time"

	"github.com/apex/log"
	"github.com/mitchellh/colorstring"

	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

// Status .
type Status string

// String satisfies the fmt.Stringer interface.
func (s Status) String() string {
	return string(s)
}

const (
	// StatusPending .
	StatusPending Status = "pending"
	// StatusProcessing .
	StatusProcessing Status = "processing"

	// StatusCancelling .
	StatusCancelling Status = "cancelling"

	// StatusCancelled .
	StatusCancelled Status = "cancelled"
	// StatusFailed .
	StatusFailed Status = "failed"
	// StatusCompleted .
	StatusCompleted Status = "completed"
)

// Transfer .
type Transfer struct {
	// ctx is the context for the transfer.
	ctx context.Context
	// cancel is used to cancel all ongoing transfer operations for the server.
	cancel *context.CancelFunc

	// Server associated with the transfer.
	Server *server.Server
	// status of the transfer.
	status *system.Atomic[Status]

	// archive is the archive that is being created for the transfer.
	archive *Archive
}

// New .
func New(ctx context.Context, s *server.Server) *Transfer {
	ctx, cancel := context.WithCancel(ctx)

	return &Transfer{
		ctx:    ctx,
		cancel: &cancel,

		Server: s,
		status: system.NewAtomic(StatusPending),
	}
}

// Context .
func (t *Transfer) Context() context.Context {
	return t.ctx
}

// Cancel .
func (t *Transfer) Cancel() {
	status := t.Status()
	if status == StatusCancelling ||
		status == StatusCancelled ||
		status == StatusCompleted ||
		status == StatusFailed {
		return
	}

	if t.cancel == nil {
		return
	}

	t.SetStatus(StatusCancelling)
	(*t.cancel)()
}

// Status .
func (t *Transfer) Status() Status {
	return t.status.Load()
}

// SetStatus .
func (t *Transfer) SetStatus(s Status) {
	// TODO: prevent certain status changes from happening.
	// If we are cancelling, then we can't go back to processing.
	t.status.Store(s)

	t.Server.Events().Publish(server.TransferStatusEvent, s)
}

// SendMessage .
func (t *Transfer) SendMessage(v string) {
	t.Server.Events().Publish(server.TransferLogsEvent, v)
}

// SendSourceMessage .
func (t *Transfer) SendSourceMessage(v string) {
	t.SendMessage(
		colorstring.Color("[yellow][bold]" + time.Now().Format(time.RFC1123) + " [Transfer System] [Source Node]:[default] " + v),
	)
}

// SendTargetMessage .
func (t *Transfer) SendTargetMessage(v string) {
	t.SendMessage(
		colorstring.Color("[yellow][bold]" + time.Now().Format(time.RFC1123) + " [Transfer System] [Target Node]:[default] " + v),
	)
}

// SourceError .
func (t *Transfer) SourceError(err error, v string) {
	t.Log().WithError(err).Error(v)
	t.SendSourceMessage(v)
}

// TargetError .
func (t *Transfer) TargetError(err error, v string) {
	t.Log().WithError(err).Error(v)
	t.SendTargetMessage(v)
}

// Log .
func (t *Transfer) Log() *log.Entry {
	if t.Server == nil {
		return log.WithField("subsystem", "transfer")
	}
	return t.Server.Log().WithField("subsystem", "transfer")
}
