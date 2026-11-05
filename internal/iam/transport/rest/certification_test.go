package rest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

func TestCertificationHTTPFlow(t *testing.T) {
	f := newRestFixture(t, domain.ChannelAdmin, false)
	role := f.createRole(t, domain.ChannelAdmin, "cert_review_role")
	if _, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "2001",
		RoleID:      role.ID,
		Actor:       "1001",
	}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	principal := principalOf(domain.ChannelAdmin, "1001", "")

	created := call(t, f.handler.CreateCertificationCampaign, newJSONRequest(t, http.MethodPost,
		"/iam/certifications", map[string]any{
			"channel":         "admin",
			"name":            "季度访问复核",
			"privileged_only": true,
			"due_at":          time.Now().UTC().Add(48 * time.Hour),
			"reason":          "季度复核",
		}), true, principal)
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	campaignID := dataID(t, decodeEnvelope(t, created))

	listed := call(t, f.handler.ListCertificationCampaigns,
		httptest.NewRequest(http.MethodGet, "/iam/certifications?limit=10", http.NoBody), true, principal)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/iam/certifications/"+campaignID+"/activate", http.NoBody)
	activateReq.SetPathValue("id", campaignID)
	activated := call(t, f.handler.ActivateCertificationCampaign, activateReq, true, principal)
	if activated.Code != http.StatusOK {
		t.Fatalf("activate status=%d body=%s", activated.Code, activated.Body.String())
	}

	tasksReq := httptest.NewRequest(http.MethodGet, "/iam/certifications/"+campaignID+"/tasks?limit=10", http.NoBody)
	tasksReq.SetPathValue("id", campaignID)
	tasks := call(t, f.handler.ListCertificationTasks, tasksReq, true, principal)
	if tasks.Code != http.StatusOK {
		t.Fatalf("tasks status=%d body=%s", tasks.Code, tasks.Body.String())
	}
}

func TestCertificationHTTPRejectsInvalidRequest(t *testing.T) {
	f := newRestFixture(t, domain.ChannelAdmin, false)
	principal := principalOf(domain.ChannelAdmin, "1001", "")

	// 名称为空：领域校验失败，应映射为 40000 参数错误。
	bad := call(t, f.handler.CreateCertificationCampaign, newJSONRequest(t, http.MethodPost,
		"/iam/certifications", map[string]any{
			"channel": "admin",
			"name":    " ",
			"due_at":  time.Now().UTC().Add(time.Hour),
		}), true, principal)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", bad.Code, bad.Body.String())
	}
	envelope := decodeEnvelope(t, bad)
	if envelope.Code != 40000 {
		t.Fatalf("biz code=%d body=%s", envelope.Code, bad.Body.String())
	}
}
