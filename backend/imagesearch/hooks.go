package imagesearch

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

// WireAttachmentHooks registers async index/delete callbacks on the attachment
// repo so create/delete (including entity cascade) update the sidecar without
// importing this package from repo (avoids an import cycle).
func WireAttachmentHooks(attachments *repo.AttachmentRepo, syncer *Syncer) {
	if attachments == nil || syncer == nil {
		return
	}
	attachments.SetPhotoIndexHooks(
		func(groupID, entityID, attachmentID uuid.UUID, path, title string) {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				att := repo.IndexablePhoto{
					ID:       attachmentID,
					EntityID: entityID,
					Path:     path,
					Title:    title,
				}
				if err := syncer.IndexPhoto(ctx, groupID, att); err != nil {
					log.Warn().Err(err).
						Str("group_id", groupID.String()).
						Str("attachment_id", attachmentID.String()).
						Msg("image-search async index failed")
				}
			}()
		},
		func(groupID, attachmentID uuid.UUID) {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := syncer.client.Delete(ctx, groupID, attachmentID); err != nil {
					log.Warn().Err(err).
						Str("group_id", groupID.String()).
						Str("attachment_id", attachmentID.String()).
						Msg("image-search async delete failed")
				}
			}()
		},
	)
}
