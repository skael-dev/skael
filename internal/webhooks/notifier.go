package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/sets"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/submissions"
)

// JobInserter is the part of the River client that enqueues in a transaction.
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Notifier turns a change into events inside the transaction that made the
// change, so a crash cannot lose an event and a rollback cannot invent one.
type Notifier struct {
	inserter JobInserter
	sets     *sets.Store
}

// NewNotifier constructs a Notifier.
func NewNotifier(inserter JobInserter, setStore *sets.Store) *Notifier {
	return &Notifier{inserter: inserter, sets: setStore}
}

// Envelope is the JSON body of every delivery.
type Envelope struct {
	ID   string    `json:"id"`
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data"`
}

// VersionData is the data of the skill version events.
type VersionData struct {
	Skill   string `json:"skill"`
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
}

// SetChangedData is the data of set.changed. It holds no skill content, so a
// webhook cannot go around a set-limited key.
type SetChangedData struct {
	SetID        string               `json:"set_id"`
	Set          string               `json:"set"`
	ResolutionID string               `json:"resolution_id"`
	Complete     bool                 `json:"complete"`
	Skills       []sets.ResolvedEntry `json:"skills"`
}

// SubmissionData is the data of submission.decided. A rejection's reason stays
// behind the API, which checks who reads it.
type SubmissionData struct {
	SubmissionID string `json:"submission_id"`
	Skill        string `json:"skill"`
	State        string `json:"state"`
	Consumer     string `json:"consumer"`
	Version      *int   `json:"version,omitempty"`
}

// emit stores one event and enqueues one job per matching endpoint. An event
// that no endpoint wants stores nothing.
func (n *Notifier) emit(ctx context.Context, tx pgx.Tx, eventType string, data any, match func(*Endpoint) bool) error {
	rows, err := tx.Query(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoints WHERE enabled`)
	if err != nil {
		return fmt.Errorf("webhooks: list endpoints: %w", err)
	}
	var targets []string
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			rows.Close()
			return err
		}
		if match(e) {
			targets = append(targets, e.ID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("webhooks: list endpoints: %w", err)
	}
	if len(targets) == 0 {
		return nil
	}

	id := uuid.NewString()
	body, err := json.Marshal(Envelope{ID: id, Type: eventType, Time: time.Now().UTC(), Data: data})
	if err != nil {
		return fmt.Errorf("webhooks: marshal event: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO webhook_events (id, type, body) VALUES ($1, $2, $3)`, id, eventType, body); err != nil {
		return fmt.Errorf("webhooks: store event: %w", err)
	}
	for _, endpoint := range targets {
		if err := n.enqueue(ctx, tx, endpoint, id); err != nil {
			return err
		}
	}
	return nil
}

func (n *Notifier) enqueue(ctx context.Context, tx pgx.Tx, endpoint, eventID string) error {
	var deliveryID string
	err := tx.QueryRow(ctx, `INSERT INTO webhook_deliveries (endpoint_id, event_id) VALUES ($1, $2) RETURNING id`, endpoint, eventID).Scan(&deliveryID)
	if err != nil {
		return fmt.Errorf("webhooks: store delivery: %w", err)
	}
	if _, err := n.inserter.InsertTx(ctx, tx, DeliveryArgs{DeliveryID: deliveryID}, nil); err != nil {
		return fmt.Errorf("webhooks: enqueue: %w", err)
	}
	return nil
}

func subscribes(eventType string) func(*Endpoint) bool {
	return func(e *Endpoint) bool { return slices.Contains(e.EventTypes, eventType) }
}

// VersionReleased implements skill.ChangeNotifier. A publish the gate holds
// never reaches it, so a held version has no event.
func (n *Notifier) VersionReleased(ctx context.Context, tx pgx.Tx, ev skill.ReleaseEvent) error {
	eventType := EventVersionPublished
	if ev.Approved {
		eventType = EventVersionApproved
	}
	data := VersionData{Skill: ev.Skill, Version: ev.Version, SHA256: ev.Checksum}
	if err := n.emit(ctx, tx, eventType, data, subscribes(eventType)); err != nil {
		return err
	}
	return n.SkillChanged(ctx, tx, ev.Skill)
}

// SkillChanged implements skill.ChangeNotifier: it rechecks each set that names
// the skill or one of its aliases.
func (n *Notifier) SkillChanged(ctx context.Context, tx pgx.Tx, skillName string) error {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT set_id FROM set_entries
		WHERE skill_name = $1 OR skill_name IN (SELECT alias FROM skill_aliases WHERE canonical = $1)
		ORDER BY set_id`, skillName)
	if err != nil {
		return fmt.Errorf("webhooks: sets of %s: %w", skillName, err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("webhooks: sets of %s: %w", skillName, err)
	}
	for _, id := range ids {
		if err := n.SetChanged(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

// SetChanged implements sets.ChangeNotifier. It emits only when the set's
// resolution id differs, so an unrelated release and an unchanged pin are quiet.
func (n *Notifier) SetChanged(ctx context.Context, tx pgx.Tx, setID string) error {
	set, res, changed, err := n.sets.Recheck(ctx, tx, setID)
	if err != nil || !changed {
		return err
	}
	data := SetChangedData{SetID: set.ID, Set: set.Name, ResolutionID: res.ID, Complete: res.Complete, Skills: res.Entries}
	return n.emit(ctx, tx, EventSetChanged, data, func(e *Endpoint) bool {
		return slices.Contains(e.EventTypes, EventSetChanged) && (len(e.SetIDs) == 0 || slices.Contains(e.SetIDs, set.ID))
	})
}

// SubmissionDecided implements submissions.ChangeNotifier.
func (n *Notifier) SubmissionDecided(ctx context.Context, tx pgx.Tx, sub *submissions.Submission) error {
	data := SubmissionData{SubmissionID: sub.ID, Skill: sub.SkillName, State: sub.State, Consumer: sub.Consumer, Version: sub.Version}
	return n.emit(ctx, tx, EventSubmissionDone, data, subscribes(EventSubmissionDone))
}

// SendTest emits a test event to one endpoint, and redeliver re-enqueues with
// the same event id: both run in their own transaction.
func (n *Notifier) SendTest(ctx context.Context, pool beginner, endpointID string) error {
	return inTx(ctx, pool, func(tx pgx.Tx) error {
		return n.emit(ctx, tx, eventTest, map[string]string{"message": "This is a test event from Skael."},
			func(e *Endpoint) bool { return e.ID == endpointID })
	})
}

// Redeliver sends a delivery again with the same event id.
func (n *Notifier) Redeliver(ctx context.Context, pool beginner, deliveryID string) (bool, error) {
	found := false
	err := inTx(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET state = 'pending', updated_at = now() WHERE id = $1`, deliveryID)
		if err != nil {
			return fmt.Errorf("webhooks: redeliver: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		found = true
		_, err = n.inserter.InsertTx(ctx, tx, DeliveryArgs{DeliveryID: deliveryID}, nil)
		return err
	})
	return found, err
}

type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

func inTx(ctx context.Context, pool beginner, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("webhooks: begin: %w", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && err != pgx.ErrTxClosed {
			log.Warn().Err(err).Msg("webhooks: rollback")
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
