package pkg


import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/pigeonlab/notifier/interr"
	"io"
	"io/ioutil"
	"net/http"
	"sync"
)

// HTTPClient is an HTTP client interface for testing and abstraction purposes.
// This is required as the standard library does not provide an interface.
// The net/*http.Client type satisfies this interface.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// BulkHTTPClient implements a classic HTTP client.
// It represents a client that sends multiple requests in bulk.
type BulkHTTPClient struct {
	HTTPClient HTTPClient
	ctx        context.Context
}

// NewBulkHTTPClient returns a new instance of BulkHTTPClient.
func NewBulkHTTPClient(client HTTPClient, ctx context.Context) *BulkHTTPClient {
	return &BulkHTTPClient{
		HTTPClient: client,
		ctx:        ctx,
	}
}

// requestData wraps a single HTTP request.
// It tracks the request's index (position).
type requestData struct {
	request *http.Request
	index   int
}

// requestFlow represents a single bulk request flow.
// A flow includes the requests, the responses, the error s(if any) and the requests' indexes.
type requestFlow struct {
	response *http.Response
	request  *http.Request
	err      error
	index    int
}

// workerChannels collects the operational channels for the HTTP client.
// - requestList: collects the HTTP requests.
// - receivedResponses: collects the HTTP RAW responses (no reading attempts yet).
// - processedResponses: collects the parsed HTTP responses: after we attempt to read the header, body and status code.
// - collectResponses: collects the responses channels.
type workerChannels struct {
	requestList        chan requestData
	receivedResponses  chan requestFlow
	processedResponses chan requestFlow
	collectResponses   chan []requestFlow
}

// newWorkerChannels returns a new instance of workerChannels.
func newWorkerChannels() workerChannels {
	return workerChannels{
		requestList:        make(chan requestData),
		receivedResponses:  make(chan requestFlow),
		processedResponses: make(chan requestFlow),
		collectResponses:   make(chan []requestFlow),
	}
}

// Do executes all the requests and tracks the behaviour in the workerChannels.
// It adds the context to each request before starting the process.
// The context is useful to handle cancellation
func (b *BulkHTTPClient) Do(bulkRequest *BulkRequest) ([]*http.Response, []error) {
	requestsCount := len(bulkRequest.requests)
	if requestsCount == 0 {
		return nil, []error{interr.ErrRequestsNotFound}
	}

	bulkRequest.responses = make([]*http.Response, requestsCount)
	bulkRequest.errors = make([]error, requestsCount)

	workerChannels := newWorkerChannels()

	stopProcessing := make(chan struct{})
	defer close(stopProcessing)

	for index, req := range bulkRequest.requests {
		bulkRequest.requests[index] = req.WithContext(b.ctx)
	}

	go b.collectProcessedResponses(
		b.ctx,
		bulkRequest,
		workerChannels.processedResponses,
		workerChannels.collectResponses,
	)

	go b.orchestrateProcesses(
		b.ctx,
		bulkRequest,
		&workerChannels,
		stopProcessing,
	)

	b.completionListener(bulkRequest, workerChannels.collectResponses)

	return bulkRequest.responses, bulkRequest.errors
}

// completionListener listens for completed responses and updates the original request at the given index.
func (b *BulkHTTPClient) completionListener(bulkRequest *BulkRequest, collectResponses chan []requestFlow) {
	responses := <-collectResponses
	for _, resParcel := range responses {
		if resParcel.err != nil {
			bulkRequest.replaceErrorAtIndex(resParcel.err, resParcel.index)
		} else {
			bulkRequest.replaceResponseAtIndex(resParcel.response, resParcel.index)
		}
	}

	close(collectResponses)
	bulkRequest.addRequestIgnoredErrors()
}

// collectProcessedResponses collects the processed responses by sending them to the final collectResponses channel.
// The collection process stops as soon as the context gets cancelled.
func (b *BulkHTTPClient) collectProcessedResponses(
	ctx context.Context,
	bulkRequest *BulkRequest,
	processedResponses <-chan requestFlow,
	collectResponses chan<- []requestFlow,
) {
	var responseList []requestFlow
LOOP:
	for done := 0; done < len(bulkRequest.requests); {
		select {
		case <-ctx.Done():
			break LOOP

		case resParcel, isOpen := <-processedResponses:
			if isOpen {
				responseList = append(responseList, resParcel)
				done++
			} else {
				break LOOP
			}
		}

	}

	collectResponses <- responseList
}

// orchestrateProcesses manages the BulkHTTPClient's' work.
// It fires and process all the requests and makes sure to wait until the whole process is completed.
// It leverages a configurable number of workers for each sub-process.
func (b *BulkHTTPClient) orchestrateProcesses(
	ctx context.Context,
	bulkRequest *BulkRequest,
	workerChannels *workerChannels,
	stopProcessing chan struct{},
) {
	var publishWg, dispatchingWg, processingWg sync.WaitGroup

	publishWg.Add(1)
	go bulkRequest.publishAllRequests(
		workerChannels.requestList,
		stopProcessing,
		&publishWg,
	)

	b.dispatchRequestsWorkers(
		bulkRequest.dispatchRequestsWorkers,
		workerChannels.requestList,
		workerChannels.receivedResponses,
		stopProcessing,
		&dispatchingWg,
	)

	b.dispatchRequestProcessorsWorkers(
		ctx,
		bulkRequest.responseProcessorWorkers,
		workerChannels.receivedResponses,
		workerChannels.processedResponses,
		stopProcessing,
		&processingWg,
	)

	publishWg.Wait()
	close(workerChannels.requestList)

	dispatchingWg.Wait()
	close(workerChannels.receivedResponses)

	processingWg.Wait()
	close(workerChannels.processedResponses)
}

// dispatchRequestsWorkers dispatches a new worker for each bulk request.
// The max number of workers is specified in the client configuration.
func (b *BulkHTTPClient) dispatchRequestsWorkers(
	dispatchRequestsWorkers int,
	requestList <-chan requestData,
	receiveResponses chan<- requestFlow,
	stopProcessing <-chan struct{},
	dispatchingWg *sync.WaitGroup,
) {

	for nWorker := 0; nWorker < dispatchRequestsWorkers; nWorker++ {
		dispatchingWg.Add(1)
		go b.fireRequests(requestList, receiveResponses, stopProcessing, dispatchingWg)
	}

}

// dispatchRequestProcessorsWorkers dispatches a new worker to process each bulk request.
// The max number of workers is specified in the client configuration.
func (b *BulkHTTPClient) dispatchRequestProcessorsWorkers(
	ctx context.Context,
	responseProcessorWorkers int,
	receiveResponses <-chan requestFlow,
	processedResponses chan<- requestFlow,
	stopProcessing <-chan struct{},
	processingWg *sync.WaitGroup,
) {

	for mWorker := 0; mWorker < responseProcessorWorkers; mWorker++ {
		processingWg.Add(1)
		go b.processRequests(ctx, receiveResponses, processedResponses, stopProcessing, processingWg)
	}

}

// fireRequests executes all the requests and accumulate the results in the receivedResponses channel.
// The process stops as soon as it receives a stop signal.
func (b *BulkHTTPClient) fireRequests(
	reqList <-chan requestData,
	receivedResponses chan<- requestFlow,
	stopProcessing <-chan struct{},
	fireWg *sync.WaitGroup,
) {

LOOP:
	for reqParcel := range reqList {
		result := b.performRequests(reqParcel)
		select {
		case receivedResponses <- result:
		case <-stopProcessing:
			if result.response != nil {
				_, _ = io.Copy(ioutil.Discard, result.response.Body)
				_ = result.response.Body.Close()
			}
			break LOOP
		}
	}

	fireWg.Done()
}

// performRequests executes the given bulk request and returns a new requestFlow.
func (b *BulkHTTPClient) performRequests(reqParcel requestData) requestFlow {
	resp, err := b.HTTPClient.Do(reqParcel.request)

	return requestFlow{
		request:  reqParcel.request,
		response: resp,
		err:      err,
		index:    reqParcel.index,
	}
}

// processRequests process all the requests in the request list channel.
// The process stops as soon as it receives a stop signal.
func (b *BulkHTTPClient) processRequests(
	ctx context.Context,
	resList <-chan requestFlow,
	processedResponses chan<- requestFlow,
	stopProcessing <-chan struct{},
	processWg *sync.WaitGroup,
) {

LOOP:
	for resParcel := range resList {
		result := b.parseResponse(ctx, resParcel)

		select {
		case processedResponses <- result:
		case <-stopProcessing:
			break LOOP
		}
	}

	processWg.Done()
}

// parseResponse attempts to read the request parts such as body, header and status code.
// It returns a Response object with a new Request object (without a timeout).
// It closes the original response to prevent the reading from a cancelled request.
func (b *BulkHTTPClient) parseResponse(ctx context.Context, res requestFlow) requestFlow {
	if res.response != nil {
		defer res.response.Body.Close()
	}

	if res.err != nil && (ctx.Err() == context.Canceled || ctx.Err() == context.DeadlineExceeded) {
		return requestFlow{err: interr.ErrIgnored, index: res.index}
	}

	if res.err != nil {
		return requestFlow{err: fmt.Errorf("http client error: %s", res.err), index: res.index}
	}

	if res.response == nil {
		return requestFlow{err: errors.New("no response received"), index: res.index}
	}

	bs, err := ioutil.ReadAll(res.response.Body)
	if err != nil {
		return requestFlow{err: fmt.Errorf("error while reading response body: %s", err), index: res.index}
	}

	body := ioutil.NopCloser(bytes.NewReader(bs))

	newResponse := http.Response{
		Body:       body,
		StatusCode: res.response.StatusCode,
		Status:     res.response.Status,
		Header:     res.response.Header,
		Request:    res.request.WithContext(context.Background()),
	}

	result := requestFlow{
		response: &newResponse,
		err:      err,
		index:    res.index,
	}

	return result
}
