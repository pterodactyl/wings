package parser

import (
	"github.com/buger/jsonparser"
)

type ReplaceValue struct {
	value     []byte
	valueType jsonparser.ValueType `json:"-"`
}

func (cv *ReplaceValue) Value() []byte {
	return cv.value
}

func (cv *ReplaceValue) String() string {
	return string(cv.value)
}

func (cv *ReplaceValue) Type() jsonparser.ValueType {
	return cv.valueType
}