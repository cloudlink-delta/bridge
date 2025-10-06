package cloudlink

import (
	"context"
	"fmt"
	"reflect"

	"github.com/goccy/go-json"
	"gorm.io/gorm/schema"
)

/*
 * This file remaps the GORM's default JSON serializer to use Go-JSON instead to improve performance.
 */

type SerializerInterface interface {
	Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue interface{}) error
	SerializerValuerInterface
}

type SerializerValuerInterface interface {
	Value(ctx context.Context, field *schema.Field, dst reflect.Value, fieldValue interface{}) (interface{}, error)
}

// GoJSONSerializer json serializer
type GoJSONSerializer struct {
}

// Scan implements serializer interface
func (GoJSONSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue interface{}) (err error) {
	fieldValue := reflect.New(field.FieldType)

	if dbValue != nil {
		var bytes []byte
		switch v := dbValue.(type) {
		case []byte:
			bytes = v
		case string:
			bytes = []byte(v)
		default:
			return fmt.Errorf("failed to unmarshal JSONB value: %#v", dbValue)
		}

		err = json.Unmarshal(bytes, fieldValue.Interface())
	}

	field.ReflectValueOf(ctx, dst).Set(fieldValue.Elem())
	return
}

// Value implements serializer interface
func (GoJSONSerializer) Value(ctx context.Context, field *schema.Field, dst reflect.Value, fieldValue interface{}) (interface{}, error) {
	return json.Marshal(fieldValue)
}
