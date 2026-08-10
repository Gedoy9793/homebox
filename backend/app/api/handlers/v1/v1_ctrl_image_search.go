package v1

import (
	"errors"
	"net/http"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/imagesearch"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

// ImageSearchHit is an EntitySummary with a similarity score from the sidecar.
type ImageSearchHit struct {
	repo.EntitySummary
	Score float64 `json:"score"`
}

func imageSearchUnavailable() error {
	return validate.NewRequestError(
		errors.New("image search is not configured; set HBOX_IMAGE_SEARCH_URL"),
		http.StatusServiceUnavailable,
	)
}

func imageSearchClient() (*imagesearch.Client, error) {
	cfg := imagesearch.LoadConfig()
	if !cfg.Enabled() {
		return nil, imageSearchUnavailable()
	}
	return imagesearch.NewClient(cfg), nil
}

// HandleEntitiesSearchByImage godoc
//
//	@Summary		Search entities by image
//	@Description	Finds entities whose photos are visually similar to the uploaded image
//	@Tags			Entities
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			file	formData	file	true	"Query image"
//	@Success		200		{object}	Results[ImageSearchHit]
//	@Failure		503		{object}	validate.ErrorResponse
//	@Router			/v1/entities/search-by-image [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleEntitiesSearchByImage() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		client, err := imageSearchClient()
		if err != nil {
			return err
		}

		err = r.ParseMultipartForm(ctrl.maxUploadSize << 20)
		if err != nil {
			return multipartFormError(err)
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			if errors.Is(err, http.ErrMissingFile) {
				return validate.NewRequestError(errors.New("file is required"), http.StatusBadRequest)
			}
			return validate.NewRequestError(err, http.StatusBadRequest)
		}
		defer func() { _ = file.Close() }()

		filename := "query.jpg"
		if header != nil && header.Filename != "" {
			filename = sanitizeAttachmentName(header.Filename)
			if filename == "" || filename == "." {
				filename = "query" + filepath.Ext(header.Filename)
			}
		}

		ctx := services.NewContext(r.Context())
		hits, err := client.Search(ctx, ctx.GID, filename, file, client.TopK())
		if err != nil {
			log.Err(err).Msg("image search failed")
			return validate.NewRequestError(err, http.StatusBadGateway)
		}

		// Deduplicate by entity, keeping the highest score.
		best := make(map[uuid.UUID]imagesearch.SearchHit, len(hits))
		order := make([]uuid.UUID, 0, len(hits))
		for _, hit := range hits {
			if hit.EntityID == uuid.Nil {
				continue
			}
			prev, ok := best[hit.EntityID]
			if !ok {
				best[hit.EntityID] = hit
				order = append(order, hit.EntityID)
				continue
			}
			if hit.Score > prev.Score {
				best[hit.EntityID] = hit
			}
		}

		results := make([]ImageSearchHit, 0, len(order))
		for _, entityID := range order {
			hit := best[entityID]
			entity, err := ctrl.repo.Entities.GetOneByGroup(ctx, ctx.GID, entityID)
			if err != nil {
				log.Debug().Err(err).
					Str("entity_id", entityID.String()).
					Msg("image search: entity not found in group; skipping")
				continue
			}
			results = append(results, ImageSearchHit{
				EntitySummary: entity.EntitySummary,
				Score:         hit.Score,
			})
		}

		return server.JSON(w, http.StatusOK, WrapResults(results))
	}
}

// HandleRebuildImageIndex godoc
//
//	@Summary		Rebuild image search index
//	@Description	Synchronizes the current group's photo attachments with the image-search sidecar
//	@Tags			Actions
//	@Produce		json
//	@Success		200	{object}	ActionAmountResult
//	@Failure		503	{object}	validate.ErrorResponse
//	@Router			/v1/actions/rebuild-image-index [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleRebuildImageIndex() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		client, err := imageSearchClient()
		if err != nil {
			return err
		}

		ctx := services.NewContext(r.Context())
		syncer := imagesearch.NewSyncer(client, ctrl.repo)
		stats, err := syncer.SyncGroup(ctx, ctx.GID)
		if err != nil {
			log.Err(err).Msg("rebuild image index failed")
			return validate.NewRequestError(err, http.StatusBadGateway)
		}

		return server.JSON(w, http.StatusOK, ActionAmountResult{
			Completed: stats.Indexed + stats.Deleted,
		})
	}
}
