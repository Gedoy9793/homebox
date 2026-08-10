package localsvc

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"

	_ "github.com/sysadminsmedia/homebox/backend/pkgs/cgofreesqlite"
)

func TestEntityPredicateReadsLabelURLs(t *testing.T) {
	accepted := []string{
		"https://homebox.example.com/item/0198f0a1-0000-7000-8000-000000000001",
		"https://homebox.example.com/location/0198f0a1-0000-7000-8000-000000000001",
		testAssetURL,
		"http://localhost:7745/a/42",
		"https://homebox.example.com/item/0198f0a1-0000-7000-8000-000000000001/",
	}
	rejected := []string{
		"",
		"https://homebox.example.com/",
		"https://homebox.example.com/item/not-a-uuid",
		"https://homebox.example.com/a/not-a-number",
		"https://homebox.example.com/a/0",
		"https://homebox.example.com/unknown/0198f0a1-0000-7000-8000-000000000001",
	}

	for _, labelURL := range accepted {
		if _, ok := entityPredicate(labelURL); !ok {
			t.Errorf("expected %q to be understood", labelURL)
		}
	}
	for _, labelURL := range rejected {
		if _, ok := entityPredicate(labelURL); ok {
			t.Errorf("expected %q to be rejected", labelURL)
		}
	}
}

// profileForTypeName exercises profileForRecord for the type-name cases, which is
// most of them.
func profileForTypeName(typeName string) string {
	return profileForRecord(entityRecord{typeName: typeName})
}

func TestProfileForTypeName(t *testing.T) {
	t.Setenv(EnvProfileMap, "")

	// Recognised without any configuration.
	for _, typeName := range []string{"Cable", "network cable", "线缆", "Patch lead"} {
		if got := profileForTypeName(typeName); got != profileCable {
			t.Errorf("expected %q to use the cable profile, got %q", typeName, got)
		}
	}

	// Anything else leaves the choice to the configuration.
	for _, typeName := range []string{"", "Item", "Shelf"} {
		if got := profileForTypeName(typeName); got != "" {
			t.Errorf("expected %q to make no choice, got %q", typeName, got)
		}
	}
}

// A location label carries the path down to it and its own description, which the
// small stock has no room for.
func TestProfileForRecordPicksTheLargeStockForLocations(t *testing.T) {
	t.Setenv(EnvProfileMap, "")

	if got := profileForRecord(entityRecord{typeName: "Shelf", isLocation: true}); got != profileLocation {
		t.Fatalf("expected a location label, got %q", got)
	}
	// A mapping still overrides it, and so does a cable-like type name.
	t.Setenv(EnvProfileMap, "Shelf=standard")
	if got := profileForRecord(entityRecord{typeName: "Shelf", isLocation: true}); got != profileStandard {
		t.Fatalf("expected the mapping to win, got %q", got)
	}
}

func TestProfileForTypeNameHonoursMapping(t *testing.T) {
	t.Setenv(EnvProfileMap, " 网线=cable , Shelf=standard ")

	if got := profileForTypeName("网线"); got != profileCable {
		t.Fatalf("expected the mapping to apply, got %q", got)
	}
	// Case-insensitive, and a mapping wins over the built-in guess.
	if got := profileForTypeName("SHELF"); got != profileStandard {
		t.Fatalf("expected the mapping to apply, got %q", got)
	}
	if got := profileForTypeName("Cable"); got != profileCable {
		t.Fatalf("expected the built-in guess to survive, got %q", got)
	}
}

func TestProfileMapIgnoresMalformedEntries(t *testing.T) {
	t.Setenv(EnvProfileMap, "no-equals-sign,Cable=cable")

	mapping := profileMap()
	if len(mapping) != 1 || mapping["cable"] != profileCable {
		t.Fatalf("unexpected mapping %+v", mapping)
	}
}

// Without Bind the service still works; it just cannot pick per type.
func TestEntityTypeNameWithoutDatabase(t *testing.T) {
	database.Store(nil)

	if got := lookupEntity(context.Background(), testAssetURL); !reflect.DeepEqual(got, entityRecord{}) {
		t.Fatalf("expected nothing without a database, got %+v", got)
	}
}

// bindTestRecord sets up a real schema with one labelled entity inside a location
// and binds it, so tests can check that a label URL leads back to the right row.
// It returns the entity's ID; its asset ID is 42, i.e. "000-042".
func bindTestRecord(t *testing.T, name string) uuid.UUID {
	t.Helper()

	ctx := context.Background()

	client, err := ent.Open("sqlite3",
		"file:localsvc-"+name+"?mode=memory&cache=shared&_fk=1&_time_format=sqlite")
	if err != nil {
		t.Fatalf("could not open the test database: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		database.Store(nil)
	})

	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("could not create the schema: %v", err)
	}

	group, err := client.Group.Create().SetName("test").Save(ctx)
	if err != nil {
		t.Fatalf("could not create a group: %v", err)
	}

	entityType, err := client.EntityType.Create().SetName("线缆").SetGroup(group).Save(ctx)
	if err != nil {
		t.Fatalf("could not create an entity type: %v", err)
	}

	location, err := client.Entity.Create().
		SetName(testLocationName).
		SetGroup(group).
		SetEntityType(entityType).
		Save(ctx)
	if err != nil {
		t.Fatalf("could not create a location: %v", err)
	}

	record, err := client.Entity.Create().
		SetName("Office AP uplink").
		SetGroup(group).
		SetEntityType(entityType).
		SetParent(location).
		SetAssetID(42).
		Save(ctx)
	if err != nil {
		t.Fatalf("could not create an entity: %v", err)
	}

	Bind(client)

	return record.ID
}

// The lookup runs against a real schema, because the whole point is that the URL
// in the QR code leads back to the right row.
func TestLookupEntityResolvesFromDatabase(t *testing.T) {
	ctx := context.Background()
	recordID := bindTestRecord(t, "lookup")

	// Both URL forms Homebox puts in a QR code have to resolve, and each has to
	// yield everything the label prints: name, location, asset ID and type.
	for _, labelURL := range []string{
		"https://homebox.example.com/item/" + recordID.String(),
		testAssetURL,
	} {
		got := lookupEntity(ctx, labelURL)

		want := entityRecord{
			typeName: "线缆",
			name:     "Office AP uplink",
			location: testLocationName,
			path:     []string{testLocationName},
			assetID:  42,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("expected %q to resolve to %+v, got %+v", labelURL, want, got)
		}
		if got.assetID.String() != "000-042" {
			t.Errorf("expected the asset ID to format as 000-042, got %q", got.assetID.String())
		}
	}

	// A record that does not exist must not fail the label.
	unknown := "https://homebox.example.com/a/999-999"
	if got := lookupEntity(ctx, unknown); !reflect.DeepEqual(got, entityRecord{}) {
		t.Errorf("expected nothing for %q, got %+v", unknown, got)
	}

	// And the whole point: this picks the label stock.
	t.Setenv(EnvProfileMap, "")
	if got := profileForRecord(lookupEntity(ctx, testAssetURL)); got != profileCable {
		t.Errorf("expected a cable flag, got %q", got)
	}
}
