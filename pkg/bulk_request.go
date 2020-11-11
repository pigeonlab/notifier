package pkg

import (
	"github.com/pigeonlab/notifier/interr"
	"net/http"
	"sync"
)

// Request represents a collection type of http requests.
type Request interface {
	Add(*http.Request) Request
}

// BulkRequest represents multiple HTTP requests in bulk.
type BulkRequest struct {
	requests                 []*http.Request
	responses                []*http.Response
	errors                   []error
	responseProcessorWorkers int
	dispatchRequestsWorkers  int
}

// NewBulkRequest returns a new BulkRequest instance.
func NewBulkRequest(requests []*http.Request, dispatchRequestsWorkers int, processResponseWorkers int) *BulkRequest {
	return &BulkRequest{
		requests:                 requests,
		dispatchRequestsWorkers:  dispatchRequestsWorkers,
		responses:                []*http.Response{},
		responseProcessorWorkers: processResponseWorkers,
	}
}

// AddRequests adds the given request to this BulkRequest.
func (b *BulkRequest) AddRequest(request *http.Request) *BulkRequest {
	b.requests = append(b.requests, request)
	return b
}

// CloseAllResponses closes all the requests' response bodies.
func (b *BulkRequest) CloseAllResponses() {
	for _, response := range b.responses {
		if response != nil {
			_ = response.Body.Close()
		}
	}
}

// publishAllRequests publishes all the requests to the requestList channel.
// It is used by the HTTP client when it starts to process the requests.
// It stops as soon as a stop signal is received.
func (b *BulkRequest) publishAllRequests(
	requestList chan<- requestData,
	stopProcessing <-chan struct{},
	publishWg *sync.WaitGroup,
) {
LOOP:
	for index := range b.requests {
		reqParcel := requestData{
			request: b.requests[index],
			index:   index,
		}

		select {
		case requestList <- reqParcel:
		case <-stopProcessing:
			break LOOP
		}
	}

	publishWg.Done()
}

// addRequestIgnoredErrors marks the responses' errors as ignored.
func (b *BulkRequest) addRequestIgnoredErrors() {
	for i, response := range b.responses {
		if response == nil && b.errors[i] == nil {
			b.errors[i] = interr.ErrIgnored
		}
	}
}

// replaceResponseAtIndex replaces the response at the given index.
func (b *BulkRequest) replaceResponseAtIndex(response *http.Response, index int) *BulkRequest {
	b.responses[index] = response
	b.errors[index] = nil
	return b
}

// replaceErrorAtIndex replaces the error at the given index.
func (b *BulkRequest) replaceErrorAtIndex(err error, index int) *BulkRequest {
	b.errors[index] = err
	b.responses[index] = nil
	return b
}
