package imagesearch

import (
	"context"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"gocloud.dev/blob"
)

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

// SyncGroup diffs local photo attachments against the sidecar and applies upserts/deletes.
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

	bucket, err := blob.OpenBucket(ctx, s.repos.Attachments.GetConnString())
	if err != nil {
		return stats, fmt.Errorf("open bucket: %w", err)
	}
	defer func() { _ = bucket.Close() }()

	// Upsert missing local photos.
	for id, att := range localByID {
		if _, ok := remoteSet[id]; ok {
			continue
		}
		if err := s.indexAttachment(ctx, bucket, groupID, att); err != nil {
			log.Warn().Err(err).
				Str("group_id", groupID.String()).
				Str("attachment_id", id.String()).
				Msg("image-search index failed")
			stats.Skipped++
			continue
		}
		stats.Indexed++
	}

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
