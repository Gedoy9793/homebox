package imagesearch

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"gocloud.dev/blob"
)

const indexWorkers = 4

// Syncer keeps the sidecar index aligned with local photo attachments.
type Syncer struct {
	client *Client
	repos  *repo.AllRepos
}

// NewSyncer constructs a Syncer.
func NewSyncer(client *Client, repos *repo.AllRepos) *Syncer {
	return &Syncer{client: client, repos: repos}
}

// SyncStats summarizes one sync pass.
type SyncStats struct {
	Indexed int `json:"indexed"`
	Deleted int `json:"deleted"`
	Skipped int `json:"skipped"`
}

// SyncAll runs SyncGroup for every group. Sidecar failures are logged and skipped
// so the main API remains unaffected.
func (s *Syncer) SyncAll(ctx context.Context) error {
	groups, err := s.repos.Groups.GetAllGroups(ctx, uuid.Nil)
	if err != nil {
		return err
	}

	for _, g := range groups {
		if _, err := s.SyncGroup(ctx, g.ID); err != nil {
			log.Warn().Err(err).
				Str("group_id", g.ID.String()).
				Msg("image-search sync skipped for group")
		}
	}
	return nil
}

// IndexPhoto pushes a single photo into the sidecar (used by create hooks).
func (s *Syncer) IndexPhoto(ctx context.Context, groupID uuid.UUID, att repo.IndexablePhoto) error {
	bucket, err := blob.OpenBucket(ctx, s.repos.Attachments.GetConnString())
	if err != nil {
		return fmt.Errorf("open bucket: %w", err)
	}
	defer func() { _ = bucket.Close() }()
	return s.indexAttachment(ctx, bucket, groupID, att)
}

// SyncGroup diffs local photo attachments against the sidecar and applies upserts/deletes.
// Already-indexed photos are skipped; only missing/extras are transferred, so steady-state
// sync stays cheap even with large libraries. First-time indexing is O(n) embeddings.
func (s *Syncer) SyncGroup(ctx context.Context, groupID uuid.UUID) (SyncStats, error) {
	var stats SyncStats

	local, err := s.repos.Attachments.ListIndexablePhotos(ctx, groupID)
	if err != nil {
		return stats, err
	}

	remoteIDs, err := s.client.ListIDs(ctx, groupID)
	if err != nil {
		return stats, fmt.Errorf("list remote ids: %w", err)
	}

	localByID := make(map[uuid.UUID]repo.IndexablePhoto, len(local))
	for _, att := range local {
		localByID[att.ID] = att
	}

	remoteSet := make(map[uuid.UUID]struct{}, len(remoteIDs))
	for _, id := range remoteIDs {
		remoteSet[id] = struct{}{}
	}

	// Delete vectors for attachments no longer present locally.
	for id := range remoteSet {
		if _, ok := localByID[id]; ok {
			continue
		}
		if err := s.client.Delete(ctx, groupID, id); err != nil {
			log.Warn().Err(err).
				Str("group_id", groupID.String()).
				Str("attachment_id", id.String()).
				Msg("image-search delete failed")
			continue
		}
		stats.Deleted++
	}

	var missing []repo.IndexablePhoto
	for id, att := range localByID {
		if _, ok := remoteSet[id]; ok {
			continue
		}
		missing = append(missing, att)
	}
	if len(missing) == 0 {
		log.Debug().
			Str("group_id", groupID.String()).
			Int("indexed", stats.Indexed).
			Int("deleted", stats.Deleted).
			Msg("image-search sync complete")
		return stats, nil
	}

	bucket, err := blob.OpenBucket(ctx, s.repos.Attachments.GetConnString())
	if err != nil {
		return stats, fmt.Errorf("open bucket: %w", err)
	}
	defer func() { _ = bucket.Close() }()

	workers := indexWorkers
	if len(missing) < workers {
		workers = len(missing)
	}
	jobs := make(chan repo.IndexablePhoto)
	var indexed, skipped atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for att := range jobs {
				if err := ctx.Err(); err != nil {
					skipped.Add(1)
					continue
				}
				if err := s.indexAttachment(ctx, bucket, groupID, att); err != nil {
					log.Warn().Err(err).
						Str("group_id", groupID.String()).
						Str("attachment_id", att.ID.String()).
						Msg("image-search index failed")
					skipped.Add(1)
					continue
				}
				indexed.Add(1)
			}
		}()
	}
	for _, att := range missing {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			stats.Indexed += int(indexed.Load())
			stats.Skipped += int(skipped.Load())
			return stats, ctx.Err()
		case jobs <- att:
		}
	}
	close(jobs)
	wg.Wait()
	stats.Indexed += int(indexed.Load())
	stats.Skipped += int(skipped.Load())

	log.Debug().
		Str("group_id", groupID.String()).
		Int("indexed", stats.Indexed).
		Int("deleted", stats.Deleted).
		Int("skipped", stats.Skipped).
		Msg("image-search sync complete")

	return stats, nil
}

func (s *Syncer) indexAttachment(ctx context.Context, bucket *blob.Bucket, groupID uuid.UUID, att repo.IndexablePhoto) error {
	reader, err := bucket.NewReader(ctx, s.repos.Attachments.GetFullPath(att.Path), nil)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	filename := att.Title
	if filename == "" {
		filename = att.ID.String()
	}

	// Bound memory for a single image push.
	limited := io.LimitReader(reader, 32<<20)
	return s.client.Index(ctx, groupID, att.ID, att.EntityID, filename, limited)
}
