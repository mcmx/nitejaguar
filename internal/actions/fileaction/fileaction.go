package fileaction

import (
	"fmt"
	"os"

	"github.com/mcmx/nitejaguar/common"
)

type fileaction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

type event struct {
	Type   string `json:"type"`   // Event type
	Result any    `json:"result"` // Generic payload for event-specific data
}

func (f *fileaction) Execute() {
	fmt.Println("Executing File Action with id:", f.data.Id)
	if f.data.Args["action"] == "create" {
		if _, err := os.Create(f.data.Args["file"]); err != nil {
			fmt.Println("Error creating file with id:", f.data.Id, err)
			result := f.sendResult("error", err.Error())
			f.events <- result
			return
		}
		fmt.Println("Creating file with id:", f.data.Id)
	} else if f.data.Args["action"] == "delete" {
		if err := os.Remove(f.data.Args["file"]); err != nil {
			fmt.Println("Error deleting file with id:", f.data.Id, err)
			result := f.sendResult("error", err.Error())
			f.events <- result
			return
		}
		result := f.sendResult("success", "File deleted successfully")
		f.events <- result
		fmt.Println("Deleting file with id:", f.data.Id)
	} else if f.data.Args["action"] == "rename" {
		if err := os.Rename(f.data.Args["file"], f.data.Args["new_file"]); err != nil {
			fmt.Println("Error renaming file with id:", f.data.Id, err)
			result := f.sendResult("error", err.Error())
			f.events <- result
			return
		}
		result := f.sendResult("success", "File renamed successfully")
		f.events <- result
		fmt.Println("Renaming file with id:", f.data.Id)
	} else {
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

func (t *fileaction) sendResult(eventType string, result string) common.ResultData {
	return common.ResultData{
		ActionID:   t.data.Id,
		ActionType: t.data.ActionType,
		ActionName: t.data.Name,
		Payload:    event{Type: eventType, Result: result},
	}
}
