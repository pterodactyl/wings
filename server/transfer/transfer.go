package transfer

import (
	"context"
	"time"

	"github.com/apex/log"
	"github.com/mitchellh/colorstring"

	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

// Status represents the current status of a transfer.
type Status string

// String satisfies the fmt.Stringer interface.
func (s Status) String() string {
	return string(s)
}

const (
	// StatusPending is the status of a transfer when it is first created.
	StatusPending Status = "pending"
	// StatusProcessing is the status of a transfer when it is currently in
	// progress, such as when the archive is being streamed to the target node.
	StatusProcessing Status = "processing"

	// StatusCancelling is the status of a transfer when it is in the process of
	// being cancelled.
	StatusCancelling Status = "cancelling"

	// StatusCancelled is the final status of a transfer when it has been
	// cancelled.
	StatusCancelled Status = "cancelled"
	// StatusFailed is the final status of a transfer when it has failed.
	StatusFailed Status = "failed"
	// StatusCompleted is the final status of a transfer when it has completed.
	StatusCompleted Status = "completed"
)

// Transfer represents a transfer of a server from one node to another.
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

// New returns a new transfer instance for the given server.
func New(ctx context.Context, s *server.Server) *Transfer {
	ctx, cancel := context.WithCancel(ctx)

	return &Transfer{
		ctx:    ctx,
		cancel: &cancel,

		Server: s,
		status: system.NewAtomic(StatusPending),
	}
}

// Context returns the context for the transfer.
func (t *Transfer) Context() context.Context {
	return t.ctx
}

// Cancel cancels the transfer.
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

// Status returns the current status of the transfer.
func (t *Transfer) Status() Status {
	return t.status.Load()
}

// SetStatus sets the status of the transfer.
func (t *Transfer) SetStatus(s Status) {
	// TODO: prevent certain status changes from happening.
	// If we are cancelling, then we can't go back to processing.
	t.status.Store(s)

	t.Server.Events().Publish(server.TransferStatusEvent, s)
}

// SendMessage sends a message to the server's console.
func (t *Transfer) SendMessage(v string) {
	t.Server.Events().Publish(
		server.TransferLogsEvent,
		colorstring.Color("[yellow][bold]"+time.Now().Format(time.RFC1123)+" [Transfer System] [Source Node]:[default] "+v),
	)
}

// Error logs an error that occurred on the source node.
func (t *Transfer) Error(err error, v string) {
	t.Log().WithError(err).Error(v)
	t.SendMessage(v)
}

// Log returns a logger for the transfer.
func (t *Transfer) Log() *log.Entry {
	if t.Server == nil {
		return log.WithField("subsystem", "transfer")
	}
	return t.Server.Log().WithField("subsystem", "transfer")
}
