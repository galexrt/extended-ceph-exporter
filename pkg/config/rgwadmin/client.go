/*
Package rgwadmin provides a copy of the radosgw admin API client of the ceph/go-ceph package
https://github.com/ceph/go-ceph

It is licensed under MIT License, see https://github.com/ceph/go-ceph/blob/master/LICENSE
*/
package rgwadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

const (
	authRegion        = "default"
	service           = "s3"
	connectionTimeout = time.Second * 3
)

var (
	errNoEndpoint  = errors.New("endpoint not set")
	errNoAccessKey = errors.New("access key not set")
	errNoSecretKey = errors.New("secret key not set")
)

const (
	queryAdminPath = "/admin"
)

func buildQueryPath(endpoint, path, args string) string {
	// Sometimes the API requires single URL key with no values
	// For instance, the Quota code uses the admin API path to "/user?quota"
	// This is done this way since url.Values does not support adding keys without values.
	//
	// So Quota code passes the begining of the query (indicated with a marker "?") in its path already, so we need to escape it
	// and add a separator key instead
	// So we can get something like "/admin/user?quota&" instead of passing two beginning query markers ("?")
	if strings.Contains(path, "?") {
		return fmt.Sprintf("%s%s%s&%s", endpoint, queryAdminPath, path, args)
	}

	return fmt.Sprintf("%s%s%s?%s", endpoint, queryAdminPath, path, args)
}

// HTTPClient interface that conforms to that of the http package's Client.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// API struct for New Client
type API struct {
	AccessKey  string
	SecretKey  string
	Endpoint   string
	HTTPClient HTTPClient
}

// New returns client for Ceph RGW
func New(endpoint, accessKey, secretKey string, httpClient HTTPClient) (*API, error) {
	// validate endpoint
	if endpoint == "" {
		return nil, errNoEndpoint
	}

	// validate access key
	if accessKey == "" {
		return nil, errNoAccessKey
	}

	// validate secret key
	if secretKey == "" {
		return nil, errNoSecretKey
	}

	// If no client is passed initialize it
	if httpClient == nil {
		httpClient = &http.Client{Timeout: connectionTimeout}
	}

	return &API{
		Endpoint:   endpoint,
		AccessKey:  accessKey,
		SecretKey:  secretKey,
		HTTPClient: httpClient,
	}, nil
}

func (api *API) Call(ctx context.Context, httpMethod, path string, args url.Values) (body []byte, err error) {
	// Build request
	request, err := http.NewRequestWithContext(ctx, httpMethod, buildQueryPath(api.Endpoint, path, args.Encode()), nil)
	if err != nil {
		return nil, err
	}

	// Build S3 authentication
	credCache := aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(api.AccessKey, api.SecretKey, ""))
	creds, err := credCache.Retrieve(ctx)
	if err != nil {
		return nil, err
	}

	signer := v4.NewSigner()
	// This was present in https://github.com/IrekFasikhov/go-rgwadmin/ but it seems that the lib works without it
	// Let's keep it here just in case something shows up
	// signer.DisableRequestBodyOverwrite = true

	// Sign in S3
	const emptyPayloadHash = "UNSIGNED-PAYLOAD"
	err = signer.SignHTTP(ctx, creds, request, emptyPayloadHash, service, authRegion, time.Now())
	if err != nil {
		return nil, err
	}

	// Send HTTP request
	resp, err := api.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Decode HTTP response
	decodedResponse, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	resp.Body = io.NopCloser(bytes.NewBuffer(decodedResponse))

	// Handle error in response
	if resp.StatusCode >= 300 {
		return nil, handleStatusError(decodedResponse)
	}

	return decodedResponse, nil
}

var unmarshalError = "failed to unmarshal radosgw http response"

// errorReason is the reason of the error
type errorReason string

// statusError is the API response when an error occurs
type statusError struct {
	Code      string `json:"Code,omitempty"`
	RequestID string `json:"RequestId,omitempty"`
	HostID    string `json:"HostId,omitempty"`
}

func handleStatusError(decodedResponse []byte) error {
	statusError := statusError{}
	err := json.Unmarshal(decodedResponse, &statusError)
	if err != nil {
		return fmt.Errorf("%s. %s. %w", unmarshalError, string(decodedResponse), err)
	}

	return statusError
}

func (e errorReason) Error() string { return string(e) }

// Is determines whether the error is known to be reported
func (e statusError) Is(target error) bool { return target == errorReason(e.Code) }

// Error returns non-empty string if there was an error.
func (e statusError) Error() string { return fmt.Sprintf("%s %s %s", e.Code, e.RequestID, e.HostID) }
