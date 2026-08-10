package localsvc

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/predicate"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

// Picking the label stock per kind of thing needs to know what kind of thing the
// label is for, and the label service is only told the URL that goes into the QR
// code. Since it runs in the Homebox process anyway, it reads the type straight
// out of the database instead of going back out over the API.
//
// Only the type name is read, and only over loopback, so no group scoping is
// applied — a type name is not information worth guarding, and the alternative
// would be threading an authenticated context through a label request that has
// none.

// EnvProfileMap maps entity type names onto label profiles, for example
// "Cable=cable,Patch lead=cable". Names are matched case-insensitively.
const EnvProfileMap = "HBOX_LOCAL_SVC_PROFILE_MAP"

// cableTypeHints are the type names that get a cable flag without any mapping
// being configured.
var cableTypeHints = []string{"cable", "线缆", "网线", "patch lead", "patch cable"}

const typeLookupTimeout = 3 * time.Second

// database is set by Bind. Nil until then, in which case every label falls back
// to the default profile.
var database atomic.Pointer[ent.Client]

// Bind gives the bundled label service read access to the database, so it can
// tell a cable from a box and choose the label stock accordingly.
//
// Called from main once the database is open. It is optional: without it labels
// still render, they just all use the default profile.
func Bind(client *ent.Client) {
	database.Store(client)

	log.Debug().Msg("Bundled label service can now pick label stock per entity type")
}

func profileForTypeName(typeName string) string {
	if typeName == "" {
		return ""
	}

	if mapped, ok := profileMap()[strings.ToLower(typeName)]; ok {
		return mapped
	}

	lowered := strings.ToLower(typeName)
	for _, hint := range cableTypeHints {
		if strings.Contains(lowered, hint) {
			return profileCable
		}
	}

	return ""
}

// entityRecord is what a label needs to know about the thing it identifies.
//
// It is read from the database rather than taken from the text labelmaker
// assembles, because that text is built for a sheet of paper: fields joined with
// newlines and an English "Location: " in front of the parent. Reading the fields
// separately lets the layout spend the 25mm it has on the values themselves.
type entityRecord struct {
	typeName string
	name     string
	location string
	assetID  repo.AssetID
}

// lookupEntity resolves the record behind a label URL such as
// https://homebox.example.com/item/<uuid> or /a/<asset-id>. A zero value means
// nothing could be resolved, which leaves the label to the text it was given.
func lookupEntity(ctx context.Context, labelURL string) entityRecord {
	client := database.Load()
	if client == nil {
		return entityRecord{}
	}

	match, ok := entityPredicate(labelURL)
	if !ok {
		return entityRecord{}
	}

	ctx, cancel := context.WithTimeout(ctx, typeLookupTimeout)
	defer cancel()

	found, err := client.Entity.Query().Where(match).WithEntityType().WithParent().Only(ctx)
	if err != nil {
		// A label for a record that cannot be read is not worth failing over; the
		// caller falls back to the text it was given.
		log.Debug().Err(err).Msg("Can not resolve the record behind a label")
		return entityRecord{}
	}

	record := entityRecord{
		name:    found.Name,
		assetID: repo.AssetID(found.AssetID),
	}

	if found.Edges.EntityType != nil {
		record.typeName = found.Edges.EntityType.Name
	}
	if found.Edges.Parent != nil {
		record.location = found.Edges.Parent.Name
	}

	return record
}

// entityPredicate turns the tail of a Homebox record URL into a query.
func entityPredicate(labelURL string) (predicate.Entity, bool) {
	trimmed := strings.TrimRight(labelURL, "/")

	kind, id, found := lastTwoSegments(trimmed)
	if !found {
		return nil, false
	}

	switch kind {
	case "item", "location":
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, false
		}

		return entity.ID(parsed), true

	case "a":
		// Asset IDs are displayed in groups, e.g. 000-042.
		assetID, err := strconv.ParseInt(strings.ReplaceAll(id, "-", ""), 10, 64)
		if err != nil || assetID <= 0 {
			return nil, false
		}

		return entity.AssetID(assetID), true

	default:
		return nil, false
	}
}

func lastTwoSegments(path string) (secondLast string, last string, ok bool) {
	end := strings.LastIndex(path, "/")
	if end < 0 {
		return "", "", false
	}

	start := strings.LastIndex(path[:end], "/")
	if start < 0 {
		return "", "", false
	}

	return path[start+1 : end], path[end+1:], true
}

// profileMap reads the operator's type-to-profile mapping.
func profileMap() map[string]string {
	raw := envString(EnvProfileMap)
	if raw == "" {
		return nil
	}

	mapping := make(map[string]string)

	for _, pair := range strings.Split(raw, ",") {
		name, profileName, found := strings.Cut(pair, "=")
		if !found {
			log.Warn().Str(EnvProfileMap, pair).Msg("Ignoring mapping, expected the form TypeName=profile")
			continue
		}

		mapping[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(profileName)
	}

	return mapping
}
