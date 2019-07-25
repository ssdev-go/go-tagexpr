// Package validator is a powerful validator that supports struct tag expression.
//
// Copyright 2019 Bytedance Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package validator

import (
	"reflect"
	"strconv"
	"strings"
	_ "unsafe"

	tagexpr "github.com/bytedance/go-tagexpr"
	"github.com/henrylee2cn/goutil"
)

const (
	// MatchExprName the name of the expression used for validation
	MatchExprName = tagexpr.DefaultExprName
	// ErrMsgExprName the name of the expression used to specify the message
	// returned when validation failed
	ErrMsgExprName = "msg"
)

// Validator struct fields validator
type Validator struct {
	vm         *tagexpr.VM
	errFactory func(failPath, msg string) error
}

// New creates a struct fields validator.
func New(tagName string) *Validator {
	v := &Validator{
		vm:         tagexpr.New(tagName),
		errFactory: defaultErrorFactory,
	}
	return v
}

// VM returns the struct tag expression interpreter.
func (v *Validator) VM() *tagexpr.VM {
	return v.vm
}

// Validate validates whether the fields of value is valid.
func (v *Validator) Validate(value interface{}) error {
	rv, ok := value.(reflect.Value)
	if !ok {
		rv = reflect.ValueOf(value)
	}
	return v.validate("", rv)
}

func (v *Validator) validate(selectorPrefix string, value reflect.Value) error {
	rv := goutil.DereferenceIfaceValue(value)
	rt := goutil.DereferenceType(rv.Type())
	rv = goutil.DereferenceValue(rv)
	switch rt.Kind() {
	case reflect.Struct:
		break

	case reflect.Slice, reflect.Array:
		count := rv.Len()
		if count == 0 {
			return nil
		}
		switch goutil.DereferenceType(rv.Type().Elem()).Kind() {
		case reflect.Struct, reflect.Interface, reflect.Slice, reflect.Array, reflect.Map:
			for i := count - 1; i >= 0; i-- {
				if err := v.validate(selectorPrefix+strconv.Itoa(i)+"/", rv.Index(i)); err != nil {
					return err
				}
			}
		default:
			return nil
		}

	case reflect.Map:
		if rv.Len() == 0 {
			return nil
		}
		var canKey, canValue bool
		rt := rv.Type()
		switch goutil.DereferenceType(rt.Key()).Kind() {
		case reflect.Struct, reflect.Interface, reflect.Slice, reflect.Array, reflect.Map:
			canKey = true
		}
		switch goutil.DereferenceType(rt.Elem()).Kind() {
		case reflect.Struct, reflect.Interface, reflect.Slice, reflect.Array, reflect.Map:
			canValue = true
		}
		if !canKey && !canValue {
			return nil
		}
		for _, key := range rv.MapKeys() {
			if canKey {
				if err := v.validate(selectorPrefix+"{k}"+"/", key); err != nil {
					return err
				}
			}
			if canValue {
				if err := v.validate(selectorPrefix+key.String()+"/", rv.MapIndex(key)); err != nil {
					return err
				}
			}
		}
		return nil
	}

	expr, err := v.vm.Run(rv)
	if err != nil {
		return err
	}
	var errSelector, errPath string
	var valid bool
	expr.Range(func(path string, es tagexpr.ExprSelector, eval func() interface{}) bool {
		if strings.Contains(path, tagexpr.ExprNameSeparator) {
			return true
		}
		valid = tagexpr.FakeBool(eval())
		if !valid {
			errSelector = es.String()
			errPath = path
		}
		return valid
	})
	if errSelector == "" {
		return nil
	}
	errMsg := expr.EvalString(errSelector + tagexpr.ExprNameSeparator + ErrMsgExprName)
	return v.errFactory(selectorPrefix+errPath, errMsg)
}

// SetErrorFactory customizes the factory of validation error.
// NOTE:
//  If errFactory==nil, the default is used
func (v *Validator) SetErrorFactory(errFactory func(failPath, msg string) error) *Validator {
	if errFactory == nil {
		errFactory = defaultErrorFactory
	}
	v.errFactory = errFactory
	return v
}

// Error validate error
type Error struct {
	FailPath, Msg string
}

// Error implements error interface.
func (e *Error) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return "invalid parameter: " + e.FailPath
}

//go:linkname defaultErrorFactory validator.defaultErrorFactory
//go:nosplit
func defaultErrorFactory(failPath, msg string) error {
	return &Error{
		FailPath: failPath,
		Msg:      msg,
	}
}
