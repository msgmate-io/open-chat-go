package Api

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
)

func ParseRequest(r *http.Request, v interface{}) error {
	contentType := r.Header.Get("Content-Type")
	switch {
	case strings.Contains(contentType, "application/json"):
		return json.NewDecoder(r.Body).Decode(v)
	case strings.Contains(contentType, "application/x-www-form-urlencoded"):
		if err := r.ParseForm(); err != nil {
			return err
		}
		return mapFormToStruct(r.PostForm, v)
	default:
		return errors.New("unsupported content type")
	}
}

func mapFormToStruct(form map[string][]string, v interface{}) error {
	val := reflect.ValueOf(v).Elem()
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		typeField := typ.Field(i)
		formKey := typeField.Tag.Get("form")
		if formKey == "" {
			formKey = strings.ToLower(typeField.Name) // default to lower case field name if no tag is provided
		}
		if formValues, ok := form[formKey]; ok && len(formValues) > 0 {
			field.SetString(formValues[0]) // assuming fields are of type string here, additional checks may be necessary
		}
	}
	return nil
}
