// Package testhelper 提供了单元测试的实用工具函数。
//
// 本包包含一些常用的辅助函数，用于简化测试任务。
//
// 这些函数旨在用于单元测试中，以提高测试的可读性并减少冗余代码。
package testhelper

import (
	"reflect"
	"strings"
	"testing"
)

// AssertEq 验证两个值是否相等
func AssertEq(t *testing.T, expected, actual interface{}, msg string) {
	t.Helper()
	if expected != actual {
		t.Errorf("%s: expected %v,but got %v\n", msg, expected, actual)
	}
}

// AssertNotEq 验证两个值是否不等
func AssertNotEq(t *testing.T, expected, actual interface{}, msg string) {
	t.Helper()
	if expected == actual {
		t.Errorf("%s: expected values to differ, but got %v", msg, actual)
	}
}

// AssertNil 检查值是否为 nil
func AssertNil(t *testing.T, obj interface{}, msg string) {
	t.Helper()
	if obj != nil {
		typ := reflect.TypeOf(obj)
		val := reflect.ValueOf(obj)
		if typ.Kind() == reflect.Ptr {
			if !val.IsNil() {
				t.Errorf("%s: expected nil, but got %v", msg, obj)
			}
		} else {
			if !val.IsZero() {
				t.Errorf("%s: expected zero, but got %v", msg, obj)
			}
		}
	}
}

// AssertNotNil 检查值是否不为 nil
func AssertNotNil(t *testing.T, obj interface{}, msg string) {
	t.Helper()
	typ := reflect.TypeOf(obj)
	val := reflect.ValueOf(obj)

	if typ.Kind() == reflect.Ptr {
		if val.IsNil() {
			t.Errorf("%s: expected non-nil pointer, but got nil pointer", msg)
		}
	} else {
		if val.IsZero() {
			t.Errorf("%s: expected non-zero value, but got zero value", msg)
		}
	}
}

// AssertError 检查函数是否返回了预期的错误
func AssertError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected error, but got nil", msg)
	}
}

// AssertContains 检查是否包含特定的字符串
func AssertContains(t *testing.T, substr, str, msg string) {
	t.Helper()
	if !strings.Contains(str, substr) {
		t.Errorf("%s: expected '%s' to be in '%s', but it wasn't", msg, substr, str)
	}
}

// AssertNotContains 检查是否不包含特定的字符串
func AssertNotContains(t *testing.T, substr, str, msg string) {
	t.Helper()
	if strings.Contains(str, substr) {
		t.Errorf("%s: expected '%s' not to be in '%s', but it was", msg, substr, str)
	}
}
