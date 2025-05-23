// Code generated by ent, DO NOT EDIT.

package ent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqlgraph"
	"entgo.io/ent/schema/field"
	"github.com/mcmx/nitejaguar/ent/predicate"
	"github.com/mcmx/nitejaguar/ent/workflow"
)

// WorkflowUpdate is the builder for updating Workflow entities.
type WorkflowUpdate struct {
	config
	hooks    []Hook
	mutation *WorkflowMutation
}

// Where appends a list predicates to the WorkflowUpdate builder.
func (wu *WorkflowUpdate) Where(ps ...predicate.Workflow) *WorkflowUpdate {
	wu.mutation.Where(ps...)
	return wu
}

// SetEnabled sets the "enabled" field.
func (wu *WorkflowUpdate) SetEnabled(b bool) *WorkflowUpdate {
	wu.mutation.SetEnabled(b)
	return wu
}

// SetNillableEnabled sets the "enabled" field if the given value is not nil.
func (wu *WorkflowUpdate) SetNillableEnabled(b *bool) *WorkflowUpdate {
	if b != nil {
		wu.SetEnabled(*b)
	}
	return wu
}

// SetJSONDefinition sets the "json_definition" field.
func (wu *WorkflowUpdate) SetJSONDefinition(s string) *WorkflowUpdate {
	wu.mutation.SetJSONDefinition(s)
	return wu
}

// SetNillableJSONDefinition sets the "json_definition" field if the given value is not nil.
func (wu *WorkflowUpdate) SetNillableJSONDefinition(s *string) *WorkflowUpdate {
	if s != nil {
		wu.SetJSONDefinition(*s)
	}
	return wu
}

// SetUpdatedAt sets the "updated_at" field.
func (wu *WorkflowUpdate) SetUpdatedAt(t time.Time) *WorkflowUpdate {
	wu.mutation.SetUpdatedAt(t)
	return wu
}

// Mutation returns the WorkflowMutation object of the builder.
func (wu *WorkflowUpdate) Mutation() *WorkflowMutation {
	return wu.mutation
}

// Save executes the query and returns the number of nodes affected by the update operation.
func (wu *WorkflowUpdate) Save(ctx context.Context) (int, error) {
	wu.defaults()
	return withHooks(ctx, wu.sqlSave, wu.mutation, wu.hooks)
}

// SaveX is like Save, but panics if an error occurs.
func (wu *WorkflowUpdate) SaveX(ctx context.Context) int {
	affected, err := wu.Save(ctx)
	if err != nil {
		panic(err)
	}
	return affected
}

// Exec executes the query.
func (wu *WorkflowUpdate) Exec(ctx context.Context) error {
	_, err := wu.Save(ctx)
	return err
}

// ExecX is like Exec, but panics if an error occurs.
func (wu *WorkflowUpdate) ExecX(ctx context.Context) {
	if err := wu.Exec(ctx); err != nil {
		panic(err)
	}
}

// defaults sets the default values of the builder before save.
func (wu *WorkflowUpdate) defaults() {
	if _, ok := wu.mutation.UpdatedAt(); !ok {
		v := workflow.UpdateDefaultUpdatedAt()
		wu.mutation.SetUpdatedAt(v)
	}
}

// check runs all checks and user-defined validators on the builder.
func (wu *WorkflowUpdate) check() error {
	if v, ok := wu.mutation.JSONDefinition(); ok {
		if err := workflow.JSONDefinitionValidator(v); err != nil {
			return &ValidationError{Name: "json_definition", err: fmt.Errorf(`ent: validator failed for field "Workflow.json_definition": %w`, err)}
		}
	}
	return nil
}

func (wu *WorkflowUpdate) sqlSave(ctx context.Context) (n int, err error) {
	if err := wu.check(); err != nil {
		return n, err
	}
	_spec := sqlgraph.NewUpdateSpec(workflow.Table, workflow.Columns, sqlgraph.NewFieldSpec(workflow.FieldID, field.TypeString))
	if ps := wu.mutation.predicates; len(ps) > 0 {
		_spec.Predicate = func(selector *sql.Selector) {
			for i := range ps {
				ps[i](selector)
			}
		}
	}
	if value, ok := wu.mutation.Enabled(); ok {
		_spec.SetField(workflow.FieldEnabled, field.TypeBool, value)
	}
	if value, ok := wu.mutation.JSONDefinition(); ok {
		_spec.SetField(workflow.FieldJSONDefinition, field.TypeString, value)
	}
	if value, ok := wu.mutation.UpdatedAt(); ok {
		_spec.SetField(workflow.FieldUpdatedAt, field.TypeTime, value)
	}
	if n, err = sqlgraph.UpdateNodes(ctx, wu.driver, _spec); err != nil {
		if _, ok := err.(*sqlgraph.NotFoundError); ok {
			err = &NotFoundError{workflow.Label}
		} else if sqlgraph.IsConstraintError(err) {
			err = &ConstraintError{msg: err.Error(), wrap: err}
		}
		return 0, err
	}
	wu.mutation.done = true
	return n, nil
}

// WorkflowUpdateOne is the builder for updating a single Workflow entity.
type WorkflowUpdateOne struct {
	config
	fields   []string
	hooks    []Hook
	mutation *WorkflowMutation
}

// SetEnabled sets the "enabled" field.
func (wuo *WorkflowUpdateOne) SetEnabled(b bool) *WorkflowUpdateOne {
	wuo.mutation.SetEnabled(b)
	return wuo
}

// SetNillableEnabled sets the "enabled" field if the given value is not nil.
func (wuo *WorkflowUpdateOne) SetNillableEnabled(b *bool) *WorkflowUpdateOne {
	if b != nil {
		wuo.SetEnabled(*b)
	}
	return wuo
}

// SetJSONDefinition sets the "json_definition" field.
func (wuo *WorkflowUpdateOne) SetJSONDefinition(s string) *WorkflowUpdateOne {
	wuo.mutation.SetJSONDefinition(s)
	return wuo
}

// SetNillableJSONDefinition sets the "json_definition" field if the given value is not nil.
func (wuo *WorkflowUpdateOne) SetNillableJSONDefinition(s *string) *WorkflowUpdateOne {
	if s != nil {
		wuo.SetJSONDefinition(*s)
	}
	return wuo
}

// SetUpdatedAt sets the "updated_at" field.
func (wuo *WorkflowUpdateOne) SetUpdatedAt(t time.Time) *WorkflowUpdateOne {
	wuo.mutation.SetUpdatedAt(t)
	return wuo
}

// Mutation returns the WorkflowMutation object of the builder.
func (wuo *WorkflowUpdateOne) Mutation() *WorkflowMutation {
	return wuo.mutation
}

// Where appends a list predicates to the WorkflowUpdate builder.
func (wuo *WorkflowUpdateOne) Where(ps ...predicate.Workflow) *WorkflowUpdateOne {
	wuo.mutation.Where(ps...)
	return wuo
}

// Select allows selecting one or more fields (columns) of the returned entity.
// The default is selecting all fields defined in the entity schema.
func (wuo *WorkflowUpdateOne) Select(field string, fields ...string) *WorkflowUpdateOne {
	wuo.fields = append([]string{field}, fields...)
	return wuo
}

// Save executes the query and returns the updated Workflow entity.
func (wuo *WorkflowUpdateOne) Save(ctx context.Context) (*Workflow, error) {
	wuo.defaults()
	return withHooks(ctx, wuo.sqlSave, wuo.mutation, wuo.hooks)
}

// SaveX is like Save, but panics if an error occurs.
func (wuo *WorkflowUpdateOne) SaveX(ctx context.Context) *Workflow {
	node, err := wuo.Save(ctx)
	if err != nil {
		panic(err)
	}
	return node
}

// Exec executes the query on the entity.
func (wuo *WorkflowUpdateOne) Exec(ctx context.Context) error {
	_, err := wuo.Save(ctx)
	return err
}

// ExecX is like Exec, but panics if an error occurs.
func (wuo *WorkflowUpdateOne) ExecX(ctx context.Context) {
	if err := wuo.Exec(ctx); err != nil {
		panic(err)
	}
}

// defaults sets the default values of the builder before save.
func (wuo *WorkflowUpdateOne) defaults() {
	if _, ok := wuo.mutation.UpdatedAt(); !ok {
		v := workflow.UpdateDefaultUpdatedAt()
		wuo.mutation.SetUpdatedAt(v)
	}
}

// check runs all checks and user-defined validators on the builder.
func (wuo *WorkflowUpdateOne) check() error {
	if v, ok := wuo.mutation.JSONDefinition(); ok {
		if err := workflow.JSONDefinitionValidator(v); err != nil {
			return &ValidationError{Name: "json_definition", err: fmt.Errorf(`ent: validator failed for field "Workflow.json_definition": %w`, err)}
		}
	}
	return nil
}

func (wuo *WorkflowUpdateOne) sqlSave(ctx context.Context) (_node *Workflow, err error) {
	if err := wuo.check(); err != nil {
		return _node, err
	}
	_spec := sqlgraph.NewUpdateSpec(workflow.Table, workflow.Columns, sqlgraph.NewFieldSpec(workflow.FieldID, field.TypeString))
	id, ok := wuo.mutation.ID()
	if !ok {
		return nil, &ValidationError{Name: "id", err: errors.New(`ent: missing "Workflow.id" for update`)}
	}
	_spec.Node.ID.Value = id
	if fields := wuo.fields; len(fields) > 0 {
		_spec.Node.Columns = make([]string, 0, len(fields))
		_spec.Node.Columns = append(_spec.Node.Columns, workflow.FieldID)
		for _, f := range fields {
			if !workflow.ValidColumn(f) {
				return nil, &ValidationError{Name: f, err: fmt.Errorf("ent: invalid field %q for query", f)}
			}
			if f != workflow.FieldID {
				_spec.Node.Columns = append(_spec.Node.Columns, f)
			}
		}
	}
	if ps := wuo.mutation.predicates; len(ps) > 0 {
		_spec.Predicate = func(selector *sql.Selector) {
			for i := range ps {
				ps[i](selector)
			}
		}
	}
	if value, ok := wuo.mutation.Enabled(); ok {
		_spec.SetField(workflow.FieldEnabled, field.TypeBool, value)
	}
	if value, ok := wuo.mutation.JSONDefinition(); ok {
		_spec.SetField(workflow.FieldJSONDefinition, field.TypeString, value)
	}
	if value, ok := wuo.mutation.UpdatedAt(); ok {
		_spec.SetField(workflow.FieldUpdatedAt, field.TypeTime, value)
	}
	_node = &Workflow{config: wuo.config}
	_spec.Assign = _node.assignValues
	_spec.ScanValues = _node.scanValues
	if err = sqlgraph.UpdateNode(ctx, wuo.driver, _spec); err != nil {
		if _, ok := err.(*sqlgraph.NotFoundError); ok {
			err = &NotFoundError{workflow.Label}
		} else if sqlgraph.IsConstraintError(err) {
			err = &ConstraintError{msg: err.Error(), wrap: err}
		}
		return nil, err
	}
	wuo.mutation.done = true
	return _node, nil
}
