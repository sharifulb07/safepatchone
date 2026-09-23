package githubapp

import (
	"context"
	"errors"
	"github.com/nats-io/nats.go"
	"safepatch/internal/application"
	"safepatch/internal/evidence"
	"safepatch/internal/platform/workqueue"
	"time"
)

// Flush publishes bounded durable work and retries expired/unacknowledged work.
// Configure stream deduplication shorter than the ten-minute recovery interval.
func (s *Store) Flush(ctx context.Context, nc *nats.Conn, stream string) error {
	if nc == nil || !evidence.ValidIdentifier(stream) {
		return application.ErrChangeWorkflow
	}
	js, err := nc.JetStream()
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT organization_id,job_id FROM github_pr_jobs WHERE status<>'DONE' AND (published_at IS NULL OR published_at<$1) ORDER BY job_id LIMIT 100`, s.now().Add(-10*time.Minute))
	if err != nil {
		return err
	}
	ids := []workqueue.Work{}
	for rows.Next() {
		var id workqueue.Work
		if err = rows.Scan(&id.OrganizationID, &id.ID); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		m, e := workqueue.Encode(workqueue.GitHubPR, id.OrganizationID, id.ID, stream)
		if e != nil {
			return e
		}
		if _, err = js.PublishMsg(m, nats.Context(ctx)); err != nil {
			return err
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE github_pr_jobs SET published_at=$2 WHERE job_id=$1 AND organization_id=$3`, id.ID, s.now(), id.OrganizationID); err != nil {
			return err
		}
	}
	return nil
}
func (w *Worker) Consume(ctx context.Context, nc *nats.Conn, stream, durable string) error {
	if w == nil || nc == nil || !evidence.ValidIdentifier(stream) || !evidence.ValidIdentifier(durable) {
		return application.ErrChangeWorkflow
	}
	js, err := nc.JetStream()
	if err != nil {
		return err
	}
	sub, err := js.PullSubscribe("", durable, nats.Bind(stream, durable))
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()
	for {
		messages, err := sub.Fetch(1, nats.Context(ctx))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, nats.ErrTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		for _, m := range messages {
			body, e := workqueue.Decode(m, workqueue.GitHubPR)
			if e != nil {
				_ = m.Term()
				continue
			}
			if e = w.Process(ctx, body.OrganizationID, body.ID); e != nil {
				_ = m.NakWithDelay(time.Minute)
				continue
			}
			if e = m.AckSync(nats.Context(ctx)); e != nil {
				return e
			}
		}
	}
}
