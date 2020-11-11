package pkg

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"
	"time"
)

const (
	ServerSleepingTime           = 50 * time.Millisecond
	TimeoutBiggerThanServerTime  = ServerSleepingTime + time.Second
	TimeoutSmallerThanServerTime = ServerSleepingTime - 40*time.Millisecond
)

func TestConcurrentRequestsAllSucceed(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	noOfRequests := 10
	timeout := TimeoutBiggerThanServerTime
	HTTPClient := &http.Client{Timeout: timeout}
	client := NewBulkHTTPClient(HTTPClient, context.Background())
	var requests []*http.Request

	for i := 0; i < noOfRequests; i++ {
		query := url.Values{}
		query.Set("kind", "fast")
		req, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", query), nil)
		require.NoError(t, err, "no errors")
		requests = append(requests, req)
	}

	bulkRequest := NewBulkRequest(requests, 10, 10)

	responses, _ := client.Do(bulkRequest)

	assert.Equal(t, noOfRequests, len(responses))

	for _, resp := range responses {
		resByte, e := ioutil.ReadAll(resp.Body)

		assert.Equal(t, "fast", string(resByte))
		assert.Nil(t, e)
	}

	bulkRequest.CloseAllResponses()
}

func TestReturnsResponsesInOrder(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	HTTPClient := &http.Client{Timeout: TimeoutBiggerThanServerTime}
	client := NewBulkHTTPClient(HTTPClient, context.Background())

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil)
	require.NoError(t, err, "no errors")

	reqThree, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	bulkRequest := NewBulkRequest([]*http.Request{reqOne, reqTwo, reqThree}, 10, 10)

	responses, _ := client.Do(bulkRequest)
	responseOne, _ := ioutil.ReadAll(responses[0].Body)
	responseTwo, _ := ioutil.ReadAll(responses[1].Body)
	responseThree, _ := ioutil.ReadAll(responses[2].Body)

	assert.Equal(t, "slow", string(responseOne))
	assert.Equal(t, "fast", string(responseTwo))
	assert.Equal(t, "slow", string(responseThree))

	bulkRequest.CloseAllResponses()
}

func TestFailureDueToClientTimeout(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	HTTPClient := &http.Client{Timeout: TimeoutSmallerThanServerTime}
	client := NewBulkHTTPClient(HTTPClient, context.Background())

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil)
	require.NoError(t, err, "no errors")

	bulkRequest := NewBulkRequest([]*http.Request{reqOne, reqTwo}, 10, 10)
	responses, errs := client.Do(bulkRequest)

	expectedClientTimeoutError := fmt.Errorf("http client error: Get \"%s?kind=slow\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)", server.URL)

	assert.Equal(t, []*http.Response{nil, nil}, responses)
	for _, e := range errs {
		assert.Equal(t, expectedClientTimeoutError, e)
	}

	bulkRequest.CloseAllResponses()
}

func TestWithMixedResultsAndMultipleWorkers(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	HTTPClient := &http.Client{Timeout: TimeoutBiggerThanServerTime}
	client := NewBulkHTTPClient(HTTPClient, context.Background())

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil) // bulk client timeout exceeded
	require.NoError(t, err, "no errors")

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil) // success
	require.NoError(t, err, "no errors")

	reqThree, err := http.NewRequest(http.MethodGet, server.URL, nil) // http client error failure
	require.NoError(t, err, "no errors")
	reqThree.URL = nil

	reqFour, err := http.NewRequest(http.MethodGet, server.URL, nil) // http client error failure
	require.NoError(t, err, "no errors")
	reqFour.URL = nil

	bulkRequest := NewBulkRequest([]*http.Request{reqOne, reqTwo, reqThree, reqFour}, 2, 2)
	responses, errs := client.Do(bulkRequest)
	defer bulkRequest.CloseAllResponses()

	assert.Equal(t, 4, len(responses))
	successResponse, _ := ioutil.ReadAll(responses[1].Body)

	assert.Equal(t, "fast", string(successResponse))
	assert.Nil(t, errs[0])
	assert.EqualError(t, errs[2], "http client error: Get \"\": http: nil Request.URL")
	assert.EqualError(t, errs[3], "http client error: Get \"\": http: nil Request.URL")
}

func TestWithMixedResultsAndSingleWorker(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	HTTPClient := &http.Client{Timeout: TimeoutBiggerThanServerTime}
	client := NewBulkHTTPClient(HTTPClient, context.Background())

	queryFast := url.Values{}
	queryFast.Set("kind", "fast")

	querySlow := url.Values{}
	querySlow.Set("kind", "slow")

	reqOne, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", querySlow), nil) // bulk client timeout exceeded
	require.NoError(t, err, "no errors")

	reqTwo, err := http.NewRequest(http.MethodGet, encodeURL(server.URL, "", queryFast), nil) // success
	require.NoError(t, err, "no errors")

	reqThree, err := http.NewRequest(http.MethodGet, server.URL, nil) // http client error failure
	require.NoError(t, err, "no errors")
	reqThree.URL = nil

	reqFour, err := http.NewRequest(http.MethodGet, server.URL, nil) // http client error failure
	require.NoError(t, err, "no errors")
	reqFour.URL = nil

	bulkRequest := NewBulkRequest([]*http.Request{reqOne, reqTwo, reqThree, reqFour}, 1, 1)
	_, errs := client.Do(bulkRequest)
	defer bulkRequest.CloseAllResponses()

	assert.Nil(t, errs[0])
	assert.Nil(t, errs[1])
	assert.EqualError(t, errs[2], "http client error: Get \"\": http: nil Request.URL")
	assert.EqualError(t, errs[3], "http client error: Get \"\": http: nil Request.URL")
}

func TestBulkClientRequestFirerAndProcessorGoroutinesAreClosed(t *testing.T) {
	server := StartMockServer()
	defer server.Close()
	timeout := TimeoutBiggerThanServerTime
	HTTPClient := &http.Client{Timeout: timeout}
	totalBulkRequests := 50
	reqsPerBulkRequest := 5
	bulkRequestsDone := 0
	var responses []*http.Response
	var errs []error

	for noOfBulkRequests := 0; noOfBulkRequests < totalBulkRequests; noOfBulkRequests++ {
		client := NewBulkHTTPClient(HTTPClient, context.Background())
		bulkRequest := newClientWithNRequests(reqsPerBulkRequest, server.URL)
		res, err := client.Do(bulkRequest)
		responses = append(responses, res...)
		errs = append(errs, err...)
		bulkRequestsDone = bulkRequestsDone + 1
		bulkRequest.CloseAllResponses()
	}

	assert.Equal(t, totalBulkRequests, bulkRequestsDone)
	assert.Equal(t, totalBulkRequests*reqsPerBulkRequest, len(responses))
	assert.Equal(t, totalBulkRequests*reqsPerBulkRequest, len(errs))

	isLessThan50 := func(x int) bool {
		if x < 50 {
			return true
		}

		fmt.Printf("CAUSE OF FAILURE: %d is greater than 20\n", x)
		return false
	}

	assert.True(t, isLessThan50(runtime.NumGoroutine()))
}

func newClientWithNRequests(n int, serverURL string) *BulkRequest {
	var requests []*http.Request
	for i := 0; i < n; i++ {
		query := url.Values{}
		req, _ := http.NewRequest(http.MethodGet, encodeURL(serverURL, "", query), nil)
		requests = append(requests, req)
	}

	bulkRequest := NewBulkRequest(requests, 10, 10)
	return bulkRequest
}
func StartMockServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(newServerHandler))
}

func newServerHandler(w http.ResponseWriter, req *http.Request) {
	performance := req.URL.Query().Get("kind")
	if len(performance) == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	if performance == "slow" {
		time.Sleep(ServerSleepingTime)
		_, _ = w.Write([]byte(performance))
		return
	}

	_, _ = w.Write([]byte(performance))
	return
}

func encodeURL(baseURL string, endpoint string, queryParams url.Values) string {
	return fmt.Sprintf("%s%s?%s", baseURL, endpoint, queryParams.Encode())
}
