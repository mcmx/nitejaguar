package fileaction

// Package fileaction implements a workflow action that creates, deletes, or renames files.
// When a matching event occurs, it emits a result to the workflow engine.

import (
	"fmt"
	"os"
	"reflect"

	"github.com/mcmx/nitejaguar/common"
)

type fileaction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

type payload struct {
	Type    string `json:"type"`               // Event type
	File    string `json:"file"`               // File name
	NewFile string `json:"new_file,omitempty"` // New file name
	Result  any    `json:"result"`             // Generic payload for event-specific data
}

func (f *fileaction) Execute(executionId string, inputs []any) {
	fmt.Println("Executing File Action with id:", f.data.Id)
	if reflect.TypeOf(f.data.Args).Kind() != reflect.Map {
		fmt.Println("[fileaction] Invalid arguments type")
		return
	}
	args := f.data.Args.(map[string]string)
	switch args["action"] {
	case "create":
		if _, err := os.Create(args["file"]); err != nil {
			fmt.Println("Error creating file with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", Result: err.Error()})
			return
		}
		f.sendResult(executionId, payload{Type: "success", File: args["file"], Result: "File created successfully"})
	case "remove":
		if err := os.Remove(args["file"]); err != nil {
			fmt.Println("Error removing file with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], Result: err.Error()})
			return
		}
		f.sendResult(executionId, payload{Type: "success", File: args["file"], Result: "File removed successfully"})
	case "rename":
		if err := os.Rename(args["file"], args["new_file"]); err != nil {
			fmt.Println("Error renaming file with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", Result: err.Error()})
			return
		}
		f.sendResult(executionId, payload{Type: "success", File: args["file"], NewFile: args["new_file"], Result: "File renamed successfully"})
	default:
		fmt.Println("Unknown action with id:", f.data.Id)
	}
}

func (f *fileaction) Stop() error {
	fmt.Println("Stopping File Action with id:", f.data.Id)
	return nil
}

func (f *fileaction) GetArgs() common.ActionArgs {
	return f.data
}

func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &fileaction{events: events, data: data}
	s.data.ActionType = "action"
	fmt.Println("Initializing File Action with id:", s.data.Id)
	return s, nil
}

func (t *fileaction) sendResult(executionId string, payload payload) {
	t.events <- common.ResultData{
		ExecutionID: executionId,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.Name,
		Payload:     payload,
	}
}
