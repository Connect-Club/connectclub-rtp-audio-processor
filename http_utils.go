package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

func getRequestParam(r *http.Request, paramName string) (string, error) {
	params, ok := r.URL.Query()[paramName]
	if !ok || len(params) != 1 {
		return "", errors.New(fmt.Sprintf("%s param error", paramName))
	}
	return params[0], nil
}

func getRequestParamInt(r *http.Request, paramName string) (int, error) {
	param, err := getRequestParam(r, paramName)
	if err != nil {
		return 0, err
	}
	paramInt, err := strconv.ParseInt(param, 10, 0)
	if err != nil {
		return 0, errors.New(fmt.Sprintf("%s param is not int", paramName))
	}
	return int(paramInt), nil
}

func getRequestParamUint64(r *http.Request, paramName string) (uint64, error) {
	param, err := getRequestParam(r, paramName)
	if err != nil {
		return 0, err
	}
	paramInt, err := strconv.ParseUint(param, 10, 64)
	if err != nil {
		return 0, errors.New(fmt.Sprintf("%s param is not uint64", paramName))
	}
	return paramInt, nil
}