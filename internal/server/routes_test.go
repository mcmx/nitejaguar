package server

import (
	"net/http"
	"testing"

	"github.com/mcmx/nitejaguar/internal/actions"
	"github.com/mcmx/nitejaguar/internal/database"

	"github.com/danielgtaylor/huma/v2/humatest"
)

func TestHandler(t *testing.T) {
	// req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, api := humatest.New(t)
	s := &Server{db: database.New(), ts: actions.TriggerService{}}
	addApiRoutes(api, s)

	resp := api.Get("/health")

	if resp.Code != http.StatusNoContent {
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
