package server

import (
	"context"
	"net/http"

	"fmt"
	"log"
	"time"

	"nitejaguar/cmd/web"
	"nitejaguar/internal/actions/common"

	"github.com/a-h/templ"
	"github.com/coder/websocket"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/mitchellh/mapstructure"
)

func (s *Server) RegisterRoutes() http.Handler {
	e := echo.New()
	config := huma.DefaultConfig(
		"NiteJaguar API",
		"1.0.0",
	)
	//	config.DocsPath = "/docs"

	api := humaecho.New(e, config)
	addApiRoutes(api, s)

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Static("/assets", "cmd/web/assets")

	e.GET("/", echo.WrapHandler(templ.Handler(web.HelloForm())))
	// e.POST("/hello", echo.WrapHandler(http.HandlerFunc(web.HelloWebHandler)))
	e.POST("/hello", s.TriggerWebHandler)

	// e.GET("/", s.HelloWorldHandler)

	e.GET("/websocket", s.websocketHandler)

	return e
}

func addApiRoutes(api huma.API, s *Server) {
	huma.Get(api, "/health", s.HealthHandler)
}

// func (s *Server) HelloWorldHandler(c echo.Context) error {
// 	resp := map[string]string{
// 		"message": "Hello World",
// 	}

// 	return c.JSON(http.StatusOK, resp)
// }

func (s *Server) TriggerWebHandler(c echo.Context) error {
	name := c.FormValue("name")
	fmt.Println("Form value Stopping Trigger:", name)
	s.ts.Stop(name)
	return c.JSON(http.StatusOK, "Ok Hello")
}

func (s *Server) HealthHandler(c context.Context, input *struct{}) (r *struct{}, e error) {
	myArgs := common.ActionArgs{
		ActionName: "filechangeTrigger",
		ActionType: "trigger",
		Name:       "Test filechange 2 trigger",
		Args:       []string{"/tmp"},
	}
	s.ts.New(myArgs)
	mapstructure.Decode(s.db.Health(), &r)
	return r, nil
}

func (s *Server) websocketHandler(c echo.Context) error {
	w := c.Response().Writer
	r := c.Request()
	socket, err := websocket.Accept(w, r, nil)

	if err != nil {
		log.Printf("could not open websocket: %v", err)
		_, _ = w.Write([]byte("could not open websocket"))
		w.WriteHeader(http.StatusInternalServerError)
		return nil
	}

	defer socket.Close(websocket.StatusGoingAway, "server closing websocket")

	ctx := r.Context()
	socketCtx := socket.CloseRead(ctx)

	for {
		payload := fmt.Sprintf("server timestamp: %d", time.Now().UnixNano())
		err := socket.Write(socketCtx, websocket.MessageText, []byte(payload))
		if err != nil {
			break
		}
		time.Sleep(time.Second * 2)
	}
	return nil
}
