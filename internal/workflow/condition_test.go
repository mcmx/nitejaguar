package workflow

import (
	"fmt"
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

// ejemplo:
func TestCondition(t *testing.T) {
	// Example usage
	dict := newConditionDictionary()

	// Add some entries
	dict.addEntry("condition1", newBooleanCondition(true), []string{"node1", "node2", "node3"})
	dict.addEntry("condition2", newComparison(10, ">=", 5), []string{"node4", "node5", "node6"})
	dict.addEntry("condition3", newBooleanCondition(false), []string{"red", "green", "blue"})
	dict.addEntry("condition4", newBooleanCondition(false), []string{"red", "green", "blue"})
	dict.removeEntry("condition4")
	_, exists := dict.getEntry("condition4")
	if exists {
		t.Error("condition4 should not exist")
	}
	// Evaluate and use entries
	strings1, _ := dict.getNextsIfTrue("condition1")
	if strings1 == nil {
		fmt.Println("Strings for condition1:", strings1) // Should print the strings
		t.Error("Strings for condition1 should not be nil")
	}

	strings2, _ := dict.getNextsIfTrue("condition2")
	c, e := dict.evaluateCondition("condition2")
	if e != nil {
		t.Errorf("Error: %v", e)
	}
	if !c {
		t.Error("Condition is false")
	}
	if strings2 == nil {
		fmt.Println("Strings for condition2:", strings2) // Should print the strings
		t.Error("Strings for condition2 should not be nil")
	}

	strings3, _ := dict.getNextsIfTrue("condition3")
	if strings3 != nil {
		t.Error("Strings for condition3 should be nil")
	}
}

func TestConditionRegexMatch(t *testing.T) {
	c := newComparison("statement-2024.pdf", "=~", `^statement-.*\.pdf$`)
	ok, err := c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected regex to match")
	}
}

func TestConditionRegexNoMatch(t *testing.T) {
	c := newComparison("invoice-2024.pdf", "=~", `^statement-.*\.pdf$`)
	ok, err := c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected regex not to match")
	}
}

func TestConditionRegexInvalidPattern(t *testing.T) {
	c := newComparison("statement.pdf", "=~", `([a-z`)
	_, err := c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err == nil {
		t.Fatal("expected error for invalid regex pattern, got nil")
	}
}

func TestConditionRegexNonStringOperand(t *testing.T) {
	c := newComparison(123, "=~", `^\d+$`)
	_, err := c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err == nil {
		t.Fatal("expected error for non-string left operand, got nil")
	}
	c = newComparison("123", "=~", 123)
	_, err = c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err == nil {
		t.Fatal("expected error for non-string right operand, got nil")
	}
}

func TestConditionRegexResultResolution(t *testing.T) {
	result := common.ResultData{Payload: map[string]any{"file": "statement-jan.pdf"}}
	c := newComparison("$result.file", "=~", `^statement-.*\.pdf$`)
	ok, err := c.evaluate(common.ActionArgs{}, nil, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected regex to match resolved $result.file")
	}
	result = common.ResultData{Payload: map[string]any{"file": "notes.txt"}}
	ok, err = c.evaluate(common.ActionArgs{}, nil, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected regex not to match resolved $result.file")
	}
}

func TestConditionDollarSyntax(t *testing.T) {
	result := common.ResultData{Payload: map[string]any{"file": "a.pdf"}}
	args := common.ActionArgs{Args: map[string]string{"path": "/tmp"}}

	// $args addresses static node arguments.
	c := newComparison("$args.path", "==", "/tmp")
	if ok, err := c.evaluate(args, nil, result); err != nil || !ok {
		t.Fatalf("expected $args.path to resolve: ok=%v err=%v", ok, err)
	}
	// $input (upstream) is not available in conditions.
	c = newComparison("$input.file", "==", "a.pdf")
	if _, err := c.evaluate(args, nil, result); err == nil {
		t.Fatal("expected error for $input.file in conditions, got nil")
	}
}

func TestConditionGlobMatch(t *testing.T) {
	c := newComparison("statement-jan.pdf", "glob", "statement-*.pdf")
	ok, err := c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected glob to match")
	}
}

func TestConditionGlobNoMatch(t *testing.T) {
	c := newComparison("invoice-jan.pdf", "glob", "statement-*.pdf")
	ok, err := c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected glob not to match")
	}
}

func TestConditionGlobInvalidPattern(t *testing.T) {
	c := newComparison("statement.pdf", "glob", "[")
	_, err := c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err == nil {
		t.Fatal("expected error for invalid glob pattern, got nil")
	}
}

func TestConditionGlobNonStringOperand(t *testing.T) {
	c := newComparison(123, "glob", "statement-*.pdf")
	_, err := c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err == nil {
		t.Fatal("expected error for non-string left operand, got nil")
	}
	c = newComparison("statement.pdf", "glob", 123)
	_, err = c.evaluate(common.ActionArgs{}, nil, common.ResultData{})
	if err == nil {
		t.Fatal("expected error for non-string right operand, got nil")
	}
}

func TestConditionGlobResultResolution(t *testing.T) {
	result := common.ResultData{Payload: map[string]any{"file": "statement-feb.pdf"}}
	c := newComparison("$result.file", "glob", "statement-*.pdf")
	ok, err := c.evaluate(common.ActionArgs{}, nil, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected glob to match resolved $result.file")
	}
	result = common.ResultData{Payload: map[string]any{"file": "notes.txt"}}
	ok, err = c.evaluate(common.ActionArgs{}, nil, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected glob not to match resolved $result.file")
	}
}

func TestConditionOmittedOrEmptyDefaultsTrue(t *testing.T) {
	args := common.ActionArgs{}
	result := common.ResultData{Payload: map[string]any{"file": "a.pdf"}}

	// Omitted condition (nil) is an unconditional route, not a panic.
	var nilCond *condition
	if ok, err := nilCond.evaluate(args, nil, result); err != nil || !ok {
		t.Fatalf("expected nil condition to default true: ok=%v err=%v", ok, err)
	}
	// Empty condition ({}) likewise defaults to true.
	if ok, err := (&condition{}).evaluate(args, nil, result); err != nil || !ok {
		t.Fatalf("expected empty condition to default true: ok=%v err=%v", ok, err)
	}
	// Explicit booleans keep working: true routes, false does not.
	if ok, err := newBooleanCondition(true).evaluate(args, nil, result); err != nil || !ok {
		t.Fatalf("expected explicit true to route: ok=%v err=%v", ok, err)
	}
	if ok, err := newBooleanCondition(false).evaluate(args, nil, result); err != nil || ok {
		t.Fatalf("expected explicit false to block: ok=%v err=%v", ok, err)
	}
}

func TestGetNextNodesRoutesWithoutCondition(t *testing.T) {
	n := Node{
		Id:         "action_mid",
		ActionType: "action",
		ActionName: "datetime",
		Conditions: &conditionDictionary{Entries: map[string]conditionEntry{
			"entry1": {Nexts: []string{"action_next"}},
		}},
	}
	nexts := n.GetNextNodes(nil, common.ResultData{ActionID: "action_mid"})
	if len(nexts) != 1 || nexts[0] != "action_next" {
		t.Fatalf("expected unconditional route to action_next, got %v", nexts)
	}
}
