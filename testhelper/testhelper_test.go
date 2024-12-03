package testhelper

import (
	"errors"
	"testing"
)

func TestAssertEqual(t *testing.T) {
	t.Run("#验证两个数据相同1", func(t *testing.T) {
		AssertEq(t, 5, 5, "两个数相等")
	})

	t.Run("#验证两个数据相同2", func(t *testing.T) {
		AssertEq(t, 5, 10, "两个数不相等")
	})

}

func TestAssertNotEqual(t *testing.T) {
	t.Run("#验证数据不相同1", func(t *testing.T) {
		AssertNotEq(t, 5, 10, "两个数不同")
	})

	t.Run("#验证数据不相同2", func(t *testing.T) {
		AssertNotEq(t, 5, 5, "两个数相同")
	})
}

func TestAssertNil(t *testing.T) {
	t.Run("#判断数据为空1", func(t *testing.T) {
		var ptr *int
		AssertNil(t, ptr, "数据为空")
	})

	t.Run("#判断数据为零2", func(t *testing.T) {
		var val int = 1
		AssertNil(t, val, "数据不为零")
	})

	t.Run("#判断数据为零1", func(t *testing.T) {
		var val int
		AssertNil(t, val, "数据为零")
	})

	t.Run("#判断数据为空2", func(t *testing.T) {
		ptr := new(int)
		AssertNil(t, ptr, "数据不为空")
	})
}

func TestAssertNotNil(t *testing.T) {
	t.Run("#验证引用数据不为空1", func(t *testing.T) {
		ptr := new(int)
		AssertNotNil(t, ptr, "引用数据不为空")
	})

	t.Run("#验证引用数据不为空2", func(t *testing.T) {
		var ptr *int
		AssertNotNil(t, ptr, "引用数据为空")
	})

	t.Run("#验证值数据不为零1", func(t *testing.T) {
		var value int = 1
		AssertNotNil(t, value, "值数据不为0")
	})

	t.Run("#验证值数据不为零2", func(t *testing.T) {
		var value int
		AssertNotNil(t, value, "值数据为0")
	})
}

func TestAssertError(t *testing.T) {
	t.Run("#验证错误信息1", func(t *testing.T) {
		err := errors.New("something went wrong")
		AssertError(t, err, "有错误信息")
	})

	t.Run("#验证错误信息2", func(t *testing.T) {
		var err error
		AssertError(t, err, "错误信息为空")
	})
}

func TestAssertContains(t *testing.T) {
	t.Run("#验证包含子串1", func(t *testing.T) {
		str := "this is a log message"
		AssertContains(t, "log", str, "log被包含")
	})

	t.Run("#验证包含子串2", func(t *testing.T) {
		str := "this is a log message"
		AssertContains(t, "error", str, "error不被包含")
	})
}

func TestAssertNotContains(t *testing.T) {
	t.Run("#验证不包含子串1", func(t *testing.T) {
		str := "this is a log message"
		AssertNotContains(t, "log", str, "log被包含")
	})

	t.Run("#验证不包含子串2", func(t *testing.T) {
		str := "this is a log message"
		AssertNotContains(t, "error", str, "error不被包含")
	})
}
