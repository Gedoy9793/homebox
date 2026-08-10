package localsvc

import (
	"context"
	"slices"
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

const (
	typeLookupTimeout = 3 * time.Second

	// maxAncestryDepth bounds the walk up the parent chain.
	maxAncestryDepth = 8
)

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

// profileForRecord picks the label stock for a record. An empty result leaves the
// choice to the configuration.
func profileForRecord(record entityRecord) string {
	// An operator's mapping wins over anything guessed here.
	if mapped, ok := profileMap()[strings.ToLower(record.typeName)]; ok && record.typeName != "" {
		return mapped
	}

	lowered := strings.ToLower(record.typeName)
	for _, hint := range cableTypeHints {
		if strings.Contains(lowered, hint) {
			return profileCable
		}
	}

	// A location label carries a path and a description, which want more room than
	// the small stock has.
	if record.isLocation {
		return profileLocation
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
	typeName    string
	name        string
	description string

	// location is the name of the parent, and path the whole chain above this
	// record from the top down. An item label only has room for the former; a
	// location label has room for the latter, and needs it — "Shelf 2" on its own
	// says nothing about which cupboard.
	location string
	path     []string

	assetID    repo.AssetID
	isLocation bool
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
		name:        found.Name,
		description: found.Description,
		assetID:     repo.AssetID(found.AssetID),
	}

	if found.Edges.EntityType != nil {
		record.typeName = found.Edges.EntityType.Name
		record.isLocation = found.Edges.EntityType.IsLocation
	}
	if found.Edges.Parent != nil {
		record.location = found.Edges.Parent.Name
		record.path = ancestry(ctx, found.Edges.Parent)
	}

	return record
}

// ancestry walks up from start and returns the names from the top down, so a
// label can say where something lives without the reader looking it up.
//
// One query per level: a home inventory is a handful of levels deep and this runs
// against the local database, so it is not worth a recursive CTE. The depth is
// capped anyway, in case a parent chain ever loops.
func ancestry(ctx context.Context, start *ent.Entity) []string {
	names := []string{start.Name}
	current := start

	for range maxAncestryDepth {
		parent, err := current.QueryParent().Only(ctx)
		if err != nil {
			// No parent, or it cannot be read: either way the chain ends here.
			break
		}

		names = append(names, parent.Name)
		current = parent
	}

	slices.Reverse(names)

	return names
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
