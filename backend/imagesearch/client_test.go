package imagesearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestClientEndpointsMatchSidecarContract(t *testing.T) {
	groupID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	attID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	entityID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	var saw struct {
		index, delete, list, search, health bool
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			saw.health = true
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "model": "test", "dim": 8})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/index":
			saw.index = true
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse index multipart: %v", err)
				http.Error(w, err.Error(), 400)
				return
			}
			for _, key := range []string{"group_id", "attachment_id", "entity_id"} {
				if r.FormValue(key) == "" {
					t.Errorf("missing form field %s", key)
				}
			}
			if _, _, err := r.FormFile("file"); err != nil {
				t.Errorf("missing file: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/index/"+groupID.String()+"/"+attID.String():
			saw.delete = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/index/"+groupID.String()+"/ids":
			saw.list = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"group_id":       groupID.String(),
				"attachment_ids": []string{attID.String()},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/search":
			saw.search = true
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse search multipart: %v", err)
				http.Error(w, err.Error(), 400)
				return
			}
			if r.FormValue("group_id") != groupID.String() {
				t.Errorf("group_id = %q", r.FormValue("group_id"))
			}
			if r.FormValue("top_k") == "" {
				t.Error("missing top_k")
			}
			if _, _, err := r.FormFile("file"); err != nil {
				t.Errorf("missing file: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{{
					"attachment_id": attID.String(),
					"entity_id":     entityID.String(),
					"score":         0.91,
				}},
			})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := NewClient(Config{URL: srv.URL, TopK: 7})
	ctx := context.Background()

	if err := client.Healthz(ctx); err != nil {
		t.Fatalf("Healthz: %v", err)
	}

	if err := client.Index(ctx, groupID, attID, entityID, "photo.jpg", strings.NewReader("fake-bytes")); err != nil {
		t.Fatalf("Index: %v", err)
	}

	ids, err := client.ListIDs(ctx, groupID)
	if err != nil {
		t.Fatalf("ListIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != attID {
		t.Fatalf("ListIDs = %v", ids)
	}

	hits, err := client.Search(ctx, groupID, "q.jpg", strings.NewReader("query"), 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].EntityID != entityID || hits[0].Score != 0.91 {
		t.Fatalf("Search hits = %+v", hits)
	}

	if err := client.Delete(ctx, groupID, attID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !saw.health || !saw.index || !saw.list || !saw.search || !saw.delete {
		t.Fatalf("not all endpoints hit: %+v", saw)
	}
}

func TestIndexMultipartFieldNames(t *testing.T) {
	// Sanity: CreateFormFile uses field name "file" matching Python UploadFile param.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("group_id", "g")
	part, err := w.CreateFormFile("file", "a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, "x")
	_ = w.Close()

	r := multipart.NewReader(&buf, w.Boundary())
	form, err := r.ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := form.File["file"]; !ok {
		t.Fatal("expected file field")
	}
}
