package binding

import (
	jsonpkg "encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/henrylee2cn/ameda"
	"github.com/tidwall/gjson"

	"github.com/ssdev-go/go-tagexpr/v2"
)

const (
	specialChar = "\x07"
)

type paramInfo struct {
	fieldSelector  string
	structField    reflect.StructField
	tagInfos       []*tagInfo
	omitIns        map[in]bool
	bindErrFactory func(failField, msg string) error
	looseZeroMode  bool
	defaultVal     []byte
}

func (p *paramInfo) name(_ in) string {
	var name string
	for _, info := range p.tagInfos {
		if info.paramIn == json {
			name = info.paramName
			break
		}
	}
	if name == "" {
		return p.structField.Name
	}
	return name
}

func (p *paramInfo) getField(expr *tagexpr.TagExpr, initZero bool) (reflect.Value, error) {
	fh, found := expr.Field(p.fieldSelector)
	if found {
		v := fh.Value(initZero)
		if v.IsValid() {
			return v, nil
		}
	}
	return reflect.Value{}, nil
}

func (p *paramInfo) bindRawBody(info *tagInfo, expr *tagexpr.TagExpr, bodyBytes []byte) error {
	if len(bodyBytes) == 0 {
		if info.required {
			return info.requiredError
		}
		return nil
	}
	v, err := p.getField(expr, true)
	if err != nil || !v.IsValid() {
		return err
	}
	v = ameda.DereferenceValue(v)
	switch v.Kind() {
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.Uint8 {
			return info.typeError
		}
		v.Set(reflect.ValueOf(bodyBytes))
		return nil
	case reflect.String:
		v.Set(reflect.ValueOf(ameda.UnsafeBytesToString(bodyBytes)))
		return nil
	default:
		return info.typeError
	}
}

func (p *paramInfo) bindPath(info *tagInfo, expr *tagexpr.TagExpr, pathParams PathParams) (bool, error) {
	if pathParams == nil {
		return false, nil
	}
	r, found := pathParams.Get(info.paramName)
	if !found {
		if info.required {
			return false, info.requiredError
		}
		return false, nil
	}
	return true, p.bindStringSlice(info, expr, []string{r})
}

func (p *paramInfo) bindQuery(info *tagInfo, expr *tagexpr.TagExpr, queryValues url.Values) (bool, error) {
	return p.bindMapStrings(info, expr, queryValues)
}

func (p *paramInfo) bindHeader(info *tagInfo, expr *tagexpr.TagExpr, header http.Header) (bool, error) {
	return p.bindMapStrings(info, expr, header)
}

func (p *paramInfo) bindCookie(info *tagInfo, expr *tagexpr.TagExpr, cookies []*http.Cookie) error {
	var r []string
	for _, c := range cookies {
		if c.Name == info.paramName {
			r = append(r, c.Value)
		}
	}
	if len(r) == 0 {
		if info.required {
			return info.requiredError
		}
		return nil
	}
	return p.bindStringSlice(info, expr, r)
}

func (p *paramInfo) bindOrRequireBody(info *tagInfo, expr *tagexpr.TagExpr, bodyCodec codec, bodyString string, postForm map[string][]string, hasDefaultVal bool) (bool, error) {
	switch bodyCodec {
	case bodyForm:
		return p.bindMapStrings(info, expr, postForm)
	case bodyJSON:
		return p.checkRequireJSON(info, expr, bodyString, hasDefaultVal)
	case bodyProtobuf:
		// It has been checked when binding, no need to check now
		return true, nil
		//err := p.checkRequireProtobuf(info, expr, false)
		// return err == nil, err
	default:
		return false, info.contentTypeError
	}
}

func (p *paramInfo) checkRequireProtobuf(info *tagInfo, expr *tagexpr.TagExpr, checkOpt bool) error {
	if checkOpt && !info.required {
		v, err := p.getField(expr, false)
		if err != nil || !v.IsValid() {
			return info.requiredError
		}
	}
	return nil
}
func (p *paramInfo) checkParamRequired(expr *tagexpr.TagExpr, bodyString, path string, requiredError error) (bool, error) {
	// recursion check inDirectStruct
	idx := strings.IndexAny(path, "\x01\x02")
	if idx > 0 {
		tmpPath := path[:idx]
		result := gjson.Get(bodyString, tmpPath)
		var err error
		result.ForEach(func(_, value gjson.Result) bool {
			_, err = p.checkParamRequired(expr, value.Raw, path[idx+2:len(path)], requiredError)
			if err != nil {
				return false
			}
			return true
		})
		if err != nil {
			return false, err
		}
		return true, nil
	}
	// check directStruct
	if !gjson.Get(bodyString, path).Exists() {
		idx := strings.LastIndex(path, ".")
		// There should be a superior but it is empty, no error is reported
		if idx > 0 && !gjson.Get(bodyString, path[:idx]).Exists() {
			return true, nil
		}
		return false, requiredError
	}
	v, err := p.getField(expr, false)
	if err != nil || !v.IsValid() {
		return false, requiredError
	}
	return true, nil
}
func (p *paramInfo) checkRequireJSON(info *tagInfo, expr *tagexpr.TagExpr, bodyString string, hasDefaultVal bool) (bool, error) {
	var requiredError error
	if info.required { // only return error if it's a required field
		requiredError = info.requiredError
	} else if !hasDefaultVal {
		return true, nil
	}
	found, err := p.checkParamRequired(expr, bodyString, info.namePath, requiredError)
	if err != nil {
		return false, requiredError
	}

	return found, nil
}

func (p *paramInfo) bindMapStrings(info *tagInfo, expr *tagexpr.TagExpr, values map[string][]string) (bool, error) {
	r, ok := values[info.paramName]
	if !ok || len(r) == 0 {
		if info.required {
			return false, info.requiredError
		}
		return false, nil
	}
	return true, p.bindStringSlice(info, expr, r)
}

// NOTE: len(a)>0
func (p *paramInfo) bindStringSlice(info *tagInfo, expr *tagexpr.TagExpr, a []string) error {
	v, err := p.getField(expr, true)
	if err != nil || !v.IsValid() {
		return err
	}

	v = ameda.DereferenceValue(v)
	switch v.Kind() {
	case reflect.String:
		v.SetString(a[0])
		return nil

	case reflect.Bool:
		var bol bool
		bol, err = strconv.ParseBool(a[0])
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetBool(bol)
			return nil
		}
	case reflect.Float32:
		var f float64
		f, err = strconv.ParseFloat(a[0], 32)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetFloat(f)
			return nil
		}
	case reflect.Float64:
		var f float64
		f, err = strconv.ParseFloat(a[0], 64)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetFloat(f)
			return nil
		}
	case reflect.Int64, reflect.Int:
		var i int64
		i, err = strconv.ParseInt(a[0], 10, 64)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetInt(i)
			return nil
		}
	case reflect.Int32:
		var i int64
		i, err = strconv.ParseInt(a[0], 10, 32)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetInt(i)
			return nil
		}
	case reflect.Int16:
		var i int64
		i, err = strconv.ParseInt(a[0], 10, 16)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetInt(i)
			return nil
		}
	case reflect.Int8:
		var i int64
		i, err = strconv.ParseInt(a[0], 10, 8)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetInt(i)
			return nil
		}
	case reflect.Uint64, reflect.Uint:
		var u uint64
		u, err = strconv.ParseUint(a[0], 10, 64)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetUint(u)
			return nil
		}
	case reflect.Uint32:
		var u uint64
		u, err = strconv.ParseUint(a[0], 10, 32)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetUint(u)
			return nil
		}
	case reflect.Uint16:
		var u uint64
		u, err = strconv.ParseUint(a[0], 10, 16)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetUint(u)
			return nil
		}
	case reflect.Uint8:
		var u uint64
		u, err = strconv.ParseUint(a[0], 10, 8)
		if err == nil || (a[0] == "" && p.looseZeroMode) {
			v.SetUint(u)
			return nil
		}
	case reflect.Slice:
		vv, retry, err := stringsToValue(v.Type().Elem(), a, p.looseZeroMode)
		if err == nil {
			v.Set(vv)
			return nil
		}
		if !retry {
			return info.typeError
		}
		fallthrough
	default:
		err = unsafeUnmarshalValue(v, a[0], p.looseZeroMode)
		if err == nil {
			return nil
		}
		// fn := typeUnmarshalFuncs[v.Type()]
		// if fn != nil {
		// 	vv, err := fn(a[0], p.looseZeroMode)
		// 	if err == nil {
		// 		v.Set(vv)
		// 		return nil
		// 	}
		// }
	}
	return info.typeError
}

func (p *paramInfo) bindDefaultVal(expr *tagexpr.TagExpr, defaultValue []byte) (bool, error) {
	if defaultValue == nil {
		return false, nil
	}
	v, err := p.getField(expr, true)
	if err != nil || !v.IsValid() {
		return false, err
	}
	return true, jsonpkg.Unmarshal(defaultValue, v.Addr().Interface())
}

// setDefaultVal preprocess the default tags and store the parsed value
func (p *paramInfo) setDefaultVal() error {
	for _, info := range p.tagInfos {
		if info.paramIn != default_val {
			continue
		}

		defaultVal := info.paramName
		st := ameda.DereferenceType(p.structField.Type)
		switch st.Kind() {
		case reflect.String:
			p.defaultVal, _ = jsonpkg.Marshal(defaultVal)
			continue
		case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
			// escape single quote and double quote, replace single quote with double quote
			defaultVal = strings.Replace(defaultVal, `"`, `\"`, -1)
			defaultVal = strings.Replace(defaultVal, `\'`, specialChar, -1)
			defaultVal = strings.Replace(defaultVal, `'`, `"`, -1)
			defaultVal = strings.Replace(defaultVal, specialChar, `'`, -1)
		}
		p.defaultVal = ameda.UnsafeStringToBytes(defaultVal)
	}
	return nil
}

var errMismatch = errors.New("type mismatch")

func stringsToValue(t reflect.Type, a []string, emptyAsZero bool) (v reflect.Value, retry bool, err error) {
	var i interface{}
	var ptrDepth int
	elemKind := t.Kind()
	for elemKind == reflect.Ptr {
		t = t.Elem()
		elemKind = t.Kind()
		ptrDepth++
	}
	switch elemKind {
	case reflect.String:
		i = a
	case reflect.Bool:
		i, err = ameda.StringsToBools(a, emptyAsZero)
	case reflect.Float32:
		i, err = ameda.StringsToFloat32s(a, emptyAsZero)
	case reflect.Float64:
		i, err = ameda.StringsToFloat64s(a, emptyAsZero)
	case reflect.Int:
		i, err = ameda.StringsToInts(a, emptyAsZero)
	case reflect.Int64:
		i, err = ameda.StringsToInt64s(a, emptyAsZero)
	case reflect.Int32:
		i, err = ameda.StringsToInt32s(a, emptyAsZero)
	case reflect.Int16:
		i, err = ameda.StringsToInt16s(a, emptyAsZero)
	case reflect.Int8:
		i, err = ameda.StringsToInt8s(a, emptyAsZero)
	case reflect.Uint:
		i, err = ameda.StringsToUints(a, emptyAsZero)
	case reflect.Uint64:
		i, err = ameda.StringsToUint64s(a, emptyAsZero)
	case reflect.Uint32:
		i, err = ameda.StringsToUint32s(a, emptyAsZero)
	case reflect.Uint16:
		i, err = ameda.StringsToUint16s(a, emptyAsZero)
	case reflect.Uint8:
		i, err = ameda.StringsToUint8s(a, emptyAsZero)
	default:
		v, err := unsafeUnmarshalSlice(t, a, emptyAsZero)
		if err == nil {
			return ameda.ReferenceSlice(v, ptrDepth), false, nil
		}
		return reflect.Value{}, true, err
		// fn := typeUnmarshalFuncs[t]
		// if fn == nil {
		// 	return reflect.Value{}, errMismatch
		// }
		// v := reflect.New(reflect.SliceOf(t)).Elem()
		// for _, s := range a {
		// 	vv, err := fn(s, emptyAsZero)
		// 	if err != nil {
		// 		return reflect.Value{}, errMismatch
		// 	}
		// 	v = reflect.Append(v, vv)
		// }
		// return ameda.ReferenceSlice(v, ptrDepth), nil
	}
	if err != nil {
		return reflect.Value{}, false, errMismatch
	}
	return ameda.ReferenceSlice(reflect.ValueOf(i), ptrDepth), false, nil
}
