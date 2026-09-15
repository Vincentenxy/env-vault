package handler

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	secretapp "env-vault/internal/application/secret"
	tagdomain "env-vault/internal/domain/tag"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stubSecretTagService struct {
	SecretTagService
	update func(uuid.UUID, []uuid.UUID) (*secretapp.GroupTags, error)
}

func (s *stubSecretTagService) Update(_ context.Context, id uuid.UUID, tags []uuid.UUID, _ string) (*secretapp.GroupTags, error) {
	return s.update(id, tags)
}

func TestSecretTagUpdateRequiresExplicitList(t *testing.T) {
	groupID := uuid.New()
	for _, tc := range []struct {
		name   string
		body   map[string]any
		code   int
		called bool
	}{
		{"omitted", map[string]any{"groupId": groupID}, -1, false},
		{"null", map[string]any{"groupId": groupID, "tagIdList": nil}, -1, false},
		{"malformed", map[string]any{"groupId": groupID, "tagIdList": []string{"bad-id"}}, -1, false},
		{"clear", map[string]any{"groupId": groupID, "tagIdList": []string{}}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			called := false
			handler := NewSecretTagHandler(&stubSecretTagService{update: func(id uuid.UUID, ids []uuid.UUID) (*secretapp.GroupTags, error) {
				called = true
				if id != groupID || ids == nil || len(ids) != 0 {
					t.Fatalf("unexpected input %v %v", id, ids)
				}
				return &secretapp.GroupTags{GroupID: id, TagList: []tagdomain.Summary{}}, nil
			}})
			router.POST("/api/v1/secret/tag/update", handler.Update)
			response := doJSON(t, router, http.MethodPost, "/api/v1/secret/tag/update", tc.body)
			body := decodeBody(t, response)
			if response.Code != http.StatusOK || body["code"] != float64(tc.code) || called != tc.called {
				t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
			}
		})
	}
}

func TestSecretTagFilterAndResponse(t *testing.T) {
	groupID, tagID := uuid.New(), uuid.New()
	for _, request := range []map[string]any{
		{"folderGroupId": uuid.New(), "tagIdList": []uuid.UUID{tagID}},
		{"projectId": uuid.New(), "folderCode": "config", "envList": []string{"dev"}, "tagIdList": []uuid.UUID{tagID}},
	} {
		router := newSecretTestEngine(&stubSecretService{listFn: func(_ context.Context, in secretapp.ListInput) ([]secretapp.SecretView, error) {
			if !reflect.DeepEqual(in.TagIDList, []uuid.UUID{tagID}) {
				t.Fatalf("missing filter: %v", in.TagIDList)
			}
			return []secretapp.SecretView{{GroupID: groupID, Key: "PASSWORD", TagList: []tagdomain.Summary{{ID: tagID, Code: "password", Name: "密码"}}}, {GroupID: uuid.New(), Key: "NO_TAG"}}, nil
		}}, nil)
		response := doJSON(t, router, http.MethodPost, "/api/v1/secret/list", request)
		body := decodeBody(t, response)
		list := body["data"].(map[string]any)["secretList"].([]any)
		if len(list[0].(map[string]any)["tagList"].([]any)) != 1 {
			t.Fatal("missing tag")
		}
		if len(list[1].(map[string]any)["tagList"].([]any)) != 0 {
			t.Fatal("untagged must return []")
		}
	}
}
