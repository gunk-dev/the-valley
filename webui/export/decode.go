package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// decodeReader bounds the work between cancellation checks while a token
// spans several reads. Closing a blocked source is the caller's responsibility.
type decodeReader struct {
	ctx context.Context
	r   io.Reader
}

func (r decodeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p[:min(len(p), 4096)])
}

// decodeValue consumes one value at a time so decoding a collection checks
// cancellation between its members. The export schema uses structs, slices,
// pointers and scalar values.
func decodeValue(ctx context.Context, dec *json.Decoder, v reflect.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if v.Kind() == reflect.Pointer || v.Kind() == reflect.Slice {
			v.SetZero()
		}
		return nil
	}
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			break
		}
		for dec.More() {
			if err := ctx.Err(); err != nil {
				return err
			}
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("expected an object field")
			}
			field := -1
			for i := 0; i < v.NumField(); i++ {
				tag := strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0]
				if tag == name {
					field = i
					break
				}
				if strings.EqualFold(tag, name) {
					field = i
				}
			}
			if field < 0 {
				return fmt.Errorf("json: unknown field %q", name)
			}
			if err := decodeValue(ctx, dec, v.Field(field)); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case reflect.Slice:
		if token != json.Delim('[') {
			break
		}
		v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		for dec.More() {
			item := reflect.New(v.Type().Elem()).Elem()
			if err := decodeValue(ctx, dec, item); err != nil {
				return err
			}
			v.Set(reflect.Append(v, item))
		}
		_, err := dec.Token()
		return err
	case reflect.String:
		if s, ok := token.(string); ok {
			v.SetString(s)
			return ctx.Err()
		}
	case reflect.Bool:
		if b, ok := token.(bool); ok {
			v.SetBool(b)
			return nil
		}
	case reflect.Int, reflect.Int64:
		if n, ok := token.(json.Number); ok {
			i, err := n.Int64()
			if err != nil {
				return err
			}
			if v.OverflowInt(i) {
				return fmt.Errorf("integer overflows %s", v.Type())
			}
			v.SetInt(i)
			return nil
		}
	}
	return fmt.Errorf("cannot decode %T into %s", token, v.Type())
}
