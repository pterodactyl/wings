package models

import (
	"database/sql"
	"encoding/json"

	"emperror.dev/errors"
)

type JsonNullString struct {
	sql.NullString
}

func (v JsonNullString) MarshalJSON() ([]byte, error) {
	if v.Valid {
		return json.Marshal(v.String)
	} else {
		return json.Marshal(nil)
	}
}

func (v *JsonNullString) UnmarshalJSON(data []byte) error {
	var s *string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.WithStack(err)
	}
	if s != nil {
		v.String = *s
	}
	v.Valid = s != nil
	return nil
}
