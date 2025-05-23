package fuego

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-fuego/fuego/internal"
)

const (
	maxBodySize = 1048576
)

type (
	// ContextNoBody is the context of the request with no body and no typed parameters.
	// It contains the path parameters, and the HTTP request.
	ContextNoBody = Context[any, any]

	// ContextWithBody is the context of the request with typed body and no typed parameters.
	// It contains the request body, the path parameters, the query parameters, and the HTTP request.
	// Please do not use a pointer type as parameters.
	ContextWithBody[B any] = Context[B, any]

	// ContextWithParams is the context of the request with no body and typed parameters.
	// It contains the path parameters, the query parameters, and the HTTP request.
	// Please do not use a pointer type as parameters.
	ContextWithParams[P any] = Context[any, P]
)

// Context is the context of the request.
// It contains the request body, the path parameters, the query parameters, and the HTTP request.
// Please do not use a pointer type as parameters.
// You can use the shortcuts [ContextNoBody], [ContextWithBody] and [ContextWithParams] if you do not need to specify the type parameters.
type Context[B, P any] interface {
	context.Context

	ValidableCtx

	// Body returns the body of the request.
	// If (*B) implements [InTransformer], it will be transformed after deserialization.
	// It caches the result, so it can be called multiple times.
	Body() (B, error)

	// MustBody works like Body, but panics if there is an error.
	MustBody() B

	// Params returns the typed parameters of the request.
	// It returns an error if the parameters are not valid.
	// Please do not use a pointer type as parameters.
	//
	// Deprecated: Not defined yet, incoming in future Fuego versions.
	Params() (P, error)

	// MustParams works like Params, but panics if there is an error.
	//
	// Deprecated: Not defined yet, incoming in future Fuego versions.
	MustParams() P

	// PathParam returns the path parameter with the given name.
	// If it does not exist, it returns an empty string.
	// Example:
	//   fuego.Get(s, "/recipes/{recipe_id}", func(c fuego.ContextNoBody) (any, error) {
	//	 	id := c.PathParam("recipe_id")
	//   	...
	//   })
	PathParam(name string) string
	// If the path parameter is not provided or is not an int, it returns 0. Use [Ctx.PathParamIntErr] if you want to know if the path parameter is erroneous.
	PathParamInt(name string) int
	PathParamIntErr(name string) (int, error)

	QueryParam(name string) string
	QueryParamArr(name string) []string
	QueryParamInt(name string) int // If the query parameter is not provided or is not an int, it returns the default given value. Use [Ctx.QueryParamIntErr] if you want to know if the query parameter is erroneous.
	QueryParamIntErr(name string) (int, error)
	QueryParamBool(name string) bool // If the query parameter is not provided or is not a bool, it returns the default given value. Use [Ctx.QueryParamBoolErr] if you want to know if the query parameter is erroneous.
	QueryParamBoolErr(name string) (bool, error)
	QueryParams() url.Values

	MainLang() string   // ex: fr. MainLang returns the main language of the request. It is the first language of the Accept-Language header. To get the main locale (ex: fr-CA), use [Ctx.MainLocale].
	MainLocale() string // ex: en-US. MainLocale returns the main locale of the request. It is the first locale of the Accept-Language header. To get the main language (ex: en), use [Ctx.MainLang].

	// Render renders the given templates with the given data.
	// Example:
	//   fuego.Get(s, "/recipes", func(c fuego.ContextNoBody) (any, error) {
	//   	recipes, _ := rs.Queries.GetRecipes(c.Context())
	//   		...
	//   	return c.Render("pages/recipes.page.html", recipes)
	//   })
	// For the Go templates reference, see https://pkg.go.dev/html/template
	//
	// [templateGlobsToOverride] is a list of templates to override.
	// For example, if you have 2 conflicting templates
	//   - with the same name "partials/aaa/nav.partial.html" and "partials/bbb/nav.partial.html"
	//   - or two templates with different names, but that define the same block "page" for example,
	// and you want to override one above the other, you can do:
	//   c.Render("admin.page.html", recipes, "partials/aaa/nav.partial.html")
	// By default, [templateToExecute] is added to the list of templates to override.
	Render(templateToExecute string, data any, templateGlobsToOverride ...string) (CtxRenderer, error)

	Cookie(name string) (*http.Cookie, error) // Get request cookie
	SetCookie(cookie http.Cookie)             // Sets response cookie
	Header(key string) string                 // Get request header
	SetHeader(key, value string)              // Sets response header

	// Returns the underlying net/http, gin or echo context.
	//
	// Usage:
	//  ctx := c.Context() // net/http: the [context.Context] of the *http.Request
	//  ctx := c.Context().(*gin.Context) // gin: Safe because the underlying context is always a [gin.Context]
	//  ctx := c.Context().(echo.Context) // echo: Safe because the underlying context is always a [echo.Context]
	Context() context.Context

	Request() *http.Request        // Request returns the underlying HTTP request.
	Response() http.ResponseWriter // Response returns the underlying HTTP response writer.

	// SetStatus sets the status code of the response.
	// Alias to http.ResponseWriter.WriteHeader.
	SetStatus(code int)

	// Redirect redirects to the given url with the given status code.
	// Example:
	//   fuego.Get(s, "/recipes", func(c fuego.ContextNoBody) (any, error) {
	//   	...
	//   	return c.Redirect(301, "/recipes-list")
	//   })
	Redirect(code int, url string) (any, error)
}

// NewNetHTTPContext returns a new context. It is used internally by Fuego. You probably want to use Ctx[B] instead.
func NewNetHTTPContext[B, P any](route BaseRoute, w http.ResponseWriter, r *http.Request, options readOptions) *netHttpContext[B, P] {
	c := &netHttpContext[B, P]{
		CommonContext: internal.CommonContext[B]{
			CommonCtx:         r.Context(),
			UrlValues:         r.URL.Query(),
			OpenAPIParams:     route.Params,
			DefaultStatusCode: route.DefaultStatusCode,
		},
		Req:         r,
		Res:         w,
		readOptions: options,
	}

	return c
}

// netHttpContext is the same as fuego.ContextNoBody, but
// has a Body. The Body type parameter represents the expected data type
// from http.Request.Body. Please do not use a pointer as a type parameter.
type netHttpContext[Body, Params any] struct {
	Res http.ResponseWriter

	fs fs.FS

	body *Body // Cache the body in request context, because it is not possible to read an HTTP request body multiple times.

	Req       *http.Request
	templates *template.Template

	serializer      Sender
	errorSerializer ErrorSender

	internal.CommonContext[Body]

	readOptions readOptions
}

var (
	// Check that netHttpContext implements Context.
	_ Context[string, struct{}] = &netHttpContext[string, struct{}]{}
	_ Context[any, any]         = &netHttpContext[any, any]{}

	// Check that netHttpContext implements ContextWithBody.
	_ ContextWithBody[any]    = &netHttpContext[any, any]{}
	_ ContextWithBody[string] = &netHttpContext[string, any]{}

	// Check that netHttpContext implements ValidableCtx.
	_ ValidableCtx = &netHttpContext[any, any]{}
)

// SetStatus sets the status code of the response.
// Alias to http.ResponseWriter.WriteHeader.
func (c netHttpContext[B, P]) SetStatus(code int) {
	c.Res.WriteHeader(code)
}

// readOptions are options for reading the request body.
type readOptions struct {
	MaxBodySize           int64
	DisallowUnknownFields bool
	LogBody               bool
}

func (c netHttpContext[B, P]) Redirect(code int, location string) (any, error) {
	http.Redirect(c.Res, c.Req, location, code)
	return nil, nil
}

// Header returns the value of the given header.
func (c netHttpContext[B, P]) Header(key string) string {
	return c.Request().Header.Get(key)
}

// HasHeader checks if the request has the given header
func (c netHttpContext[B, P]) HasHeader(key string) bool {
	return c.Header(key) != ""
}

// SetHeader sets the value of the given header
func (c netHttpContext[B, P]) SetHeader(key, value string) {
	c.Response().Header().Set(key, value)
}

// Cookie get request cookie
func (c netHttpContext[B, P]) Cookie(name string) (*http.Cookie, error) {
	return c.Request().Cookie(name)
}

// HasCookie checks if the request has the given cookie
func (c netHttpContext[B, P]) HasCookie(name string) bool {
	_, err := c.Cookie(name)
	return err == nil
}

// SetCookie response cookie
func (c netHttpContext[B, P]) SetCookie(cookie http.Cookie) {
	http.SetCookie(c.Response(), &cookie)
}

// Render renders the given templates with the given data.
// It returns just an empty string, because the response is written directly to the http.ResponseWriter.
//
// Init templates if not already done.
// This has the side effect of making the Render method static, meaning
// that the templates will be parsed only once, removing
// the need to parse the templates on each request but also preventing
// to dynamically use new templates.
func (c netHttpContext[B, P]) Render(templateToExecute string, data any, layoutsGlobs ...string) (CtxRenderer, error) {
	return &StdRenderer{
		templateToExecute: templateToExecute,
		templates:         c.templates,
		layoutsGlobs:      layoutsGlobs,
		fs:                c.fs,
		data:              data,
	}, nil
}

// PathParam returns the path parameters of the request.
func (c netHttpContext[B, P]) PathParam(name string) string {
	return c.Req.PathValue(name)
}

type PathParamNotFoundError struct {
	ParamName string
}

func (e PathParamNotFoundError) Error() string {
	return fmt.Sprintf("path param %s not found", e.ParamName)
}

func (e PathParamNotFoundError) StatusCode() int { return http.StatusUnprocessableEntity }

func (e PathParamNotFoundError) DetailMsg() string {
	return e.Error()
}

type PathParamInvalidTypeError struct {
	Err          error
	ParamName    string
	ParamValue   string
	ExpectedType string
}

func (e PathParamInvalidTypeError) Error() string {
	return fmt.Sprintf("%s: %s", e.DetailMsg(), e.Err)
}

func (e PathParamInvalidTypeError) StatusCode() int { return http.StatusUnprocessableEntity }

func (e PathParamInvalidTypeError) DetailMsg() string {
	return fmt.Sprintf("path param %s=%s is not of type %s", e.ParamName, e.ParamValue, e.ExpectedType)
}

type ContextWithPathParam interface {
	PathParam(name string) string
}

func PathParamIntErr(c ContextWithPathParam, name string) (int, error) {
	param := c.PathParam(name)
	if param == "" {
		return 0, PathParamNotFoundError{ParamName: name}
	}

	i, err := strconv.Atoi(param)
	if err != nil {
		return 0, PathParamInvalidTypeError{
			ParamName:    name,
			ParamValue:   param,
			ExpectedType: "int",
			Err:          err,
		}
	}

	return i, nil
}

func (c netHttpContext[B, P]) PathParamIntErr(name string) (int, error) {
	return PathParamIntErr(c, name)
}

// PathParamInt returns the path parameter with the given name as an int.
// If the query parameter does not exist, or if it is not an int, it returns 0.
func (c netHttpContext[B, P]) PathParamInt(name string) int {
	param, _ := PathParamIntErr(c, name)
	return param
}

func (c netHttpContext[B, P]) MainLang() string {
	return strings.Split(c.MainLocale(), "-")[0]
}

func (c netHttpContext[B, P]) MainLocale() string {
	return strings.Split(c.Req.Header.Get("Accept-Language"), ",")[0]
}

// Request returns the HTTP request.
func (c netHttpContext[B, P]) Request() *http.Request {
	return c.Req
}

// Response returns the HTTP response writer.
func (c netHttpContext[B, P]) Response() http.ResponseWriter {
	return c.Res
}

// MustBody works like Body, but panics if there is an error.
func (c *netHttpContext[B, P]) MustBody() B {
	b, err := c.Body()
	if err != nil {
		panic(err)
	}
	return b
}

// Body returns the body of the request.
// If (*B) implements [InTransformer], it will be transformed after deserialization.
// It caches the result, so it can be called multiple times.
// The reason the body is cached is that it is impossible to read an HTTP request body multiple times, not because of performance.
// For decoding, it uses the Content-Type header. If it is not set, defaults to application/json.
func (c *netHttpContext[B, P]) Body() (B, error) {
	if c.body != nil {
		return *c.body, nil
	}

	body, err := body(*c)
	c.body = &body
	return body, err
}

func bitSize(kind reflect.Kind) int {
	switch kind {
	case reflect.Uint8, reflect.Int8:
		return 8
	case reflect.Uint16, reflect.Int16:
		return 16
	case reflect.Uint32, reflect.Int32, reflect.Float32:
		return 32
	case reflect.Uint, reflect.Int:
		return strconv.IntSize
	}
	return 64
}

// setParamValue sets a value to a reflect.Value based on its kind
func setParamValue(value reflect.Value, paramValue string, kind reflect.Kind) error {
	switch kind {
	case reflect.String:
		value.SetString(paramValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		intValue, err := strconv.ParseInt(paramValue, 10, bitSize(kind))
		if err != nil {
			return fmt.Errorf("cannot convert %s to %s: %w", paramValue, kind, err)
		}
		value.SetInt(intValue)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uintValue, err := strconv.ParseUint(paramValue, 10, bitSize(kind))
		if err != nil {
			return fmt.Errorf("cannot convert %s to %s: %w", paramValue, kind, err)
		}
		value.SetUint(uintValue)
	case reflect.Float32, reflect.Float64:
		floatValue, err := strconv.ParseFloat(paramValue, bitSize(kind))
		if err != nil {
			return fmt.Errorf("cannot convert %s to %s: %w", paramValue, kind, err)
		}
		value.SetFloat(floatValue)
	case reflect.Bool:
		boolValue, err := strconv.ParseBool(paramValue)
		if err != nil {
			return fmt.Errorf("cannot convert %s to bool: %w", paramValue, err)
		}
		value.SetBool(boolValue)
	default:
		return fmt.Errorf("unsupported type %s", kind)
	}
	return nil
}

func (c *netHttpContext[B, P]) Params() (P, error) {
	p := new(P)

	paramsType := reflect.TypeOf(p).Elem()
	if paramsType.Kind() != reflect.Struct {
		return *p, fmt.Errorf("params must be a struct, got %T", *p)
	}
	paramsValue := reflect.ValueOf(p).Elem()

	for i := range paramsType.NumField() {
		field := paramsType.Field(i)
		fieldValue := paramsValue.Field(i)

		// Process query parameters
		if tag := field.Tag.Get("query"); tag != "" {
			// Handle slice/array types
			switch field.Type.Kind() {
			case reflect.Slice, reflect.Array:
				paramValues := c.QueryParamArr(tag)
				if len(paramValues) == 0 {
					continue
				}

				sliceType := field.Type.Elem()
				slice := reflect.MakeSlice(field.Type, len(paramValues), len(paramValues))

				for j, paramValue := range paramValues {
					if err := setParamValue(slice.Index(j), paramValue, sliceType.Kind()); err != nil {
						return *p, err
					}
				}
				fieldValue.Set(slice)
			default:
				// Handle single value
				paramValue := c.QueryParam(tag)
				if paramValue == "" {
					continue
				}
				err := setParamValue(fieldValue, paramValue, field.Type.Kind())
				if err != nil {
					return *p, err
				}
			}
		} else if tag := field.Tag.Get("header"); tag != "" {
			// Process header parameters
			paramValue := c.Header(tag)
			if paramValue == "" {
				continue
			}
			err := setParamValue(fieldValue, paramValue, field.Type.Kind())
			if err != nil {
				return *p, err
			}
		}
	}

	return *p, nil
}

func (c *netHttpContext[B, P]) MustParams() P {
	params, err := c.Params()
	if err != nil {
		panic(err)
	}
	return params
}

// Serialize serializes the given data to the response. It uses the Content-Type header to determine the serialization format.
func (c netHttpContext[B, P]) Serialize(data any) error {
	if c.serializer == nil {
		return Send(c.Res, c.Req, data)
	}
	return c.serializer(c.Res, c.Req, data)
}

// SerializeError serializes the given error to the response. It uses the Content-Type header to determine the serialization format.
func (c netHttpContext[B, P]) SerializeError(err error) {
	if c.errorSerializer == nil {
		SendError(c.Res, c.Req, err)
		return
	}
	c.errorSerializer(c.Res, c.Req, err)
}

// SetDefaultStatusCode sets the default status code of the response.
func (c netHttpContext[B, P]) SetDefaultStatusCode() {
	if c.DefaultStatusCode != 0 {
		c.SetStatus(c.DefaultStatusCode)
	}
}

func body[B, P any](c netHttpContext[B, P]) (B, error) {
	// Limit the size of the request body.
	if c.readOptions.MaxBodySize != 0 {
		c.Req.Body = http.MaxBytesReader(nil, c.Req.Body, c.readOptions.MaxBodySize)
	}

	timeDeserialize := time.Now()

	var body B
	var err error
	switch c.Req.Header.Get("Content-Type") {
	case "text/plain":
		s, errReadingString := readString[string](c.Req.Context(), c.Req.Body, c.readOptions)
		body = any(s).(B)
		err = errReadingString
	case "application/x-www-form-urlencoded", "multipart/form-data":
		body, err = readURLEncoded[B](c.Req, c.readOptions)
	case "application/xml":
		body, err = readXML[B](c.Req.Context(), c.Req.Body, c.readOptions)
	case "application/x-yaml", "text/yaml; charset=utf-8", "application/yaml": // https://www.rfc-editor.org/rfc/rfc9512.html
		body, err = readYAML[B](c.Req.Context(), c.Req.Body, c.readOptions)
	case "application/octet-stream":
		// Read c.Req Body to bytes
		bytes, err := io.ReadAll(c.Req.Body)
		if err != nil {
			return body, err
		}
		respBytes, ok := any(bytes).(B)
		if !ok {
			return body, fmt.Errorf("could not convert bytes to %T. To read binary data from the request, use []byte as the body type", body)
		}
		body = respBytes
	default:
		body, err = readJSON[B](c.Req.Context(), c.Req.Body, c.readOptions)
	}

	c.Res.Header().Add("Server-Timing", Timing{"deserialize", "controller > deserialize", time.Since(timeDeserialize)}.String())

	return body, err
}
