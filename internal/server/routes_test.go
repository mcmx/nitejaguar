package server

import (
	"net/http"
	"testing"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"

	"github.com/danielgtaylor/huma/v2/humatest"
)

func TestHandler(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	// req := httptest.NewRequest(http.MethodGet, "/", nil)
	db := database.New()
	_, api := humatest.New(t)
	s := &Server{db: db, wm: workflow.NewWorkflowManager(false, db)}
	addApiRoutes(api, s)

	resp := api.Get("/health")

	if resp.Code != http.StatusOK {
		t.Errorf("handler() wrong status code = %v", resp.Code)
		return
	}
	//expected := map[string]string{"status": "up"}
	//var actual map[string]string
	// Decode the response body into the actual map
	//if err := json.NewDecoder(resp.Body).Decode(&actual); err != nil {
	//	t.Errorf("handler() error decoding response body: %v", err)
	//	return
	//}
	// Compare the decoded response with the expected value
	//if !reflect.DeepEqual(expected["status"], actual["status"]) {
	//	t.Errorf("handler() wrong response body. expected = %v, actual = %v", expected, actual)
	//	return
	//}
}
