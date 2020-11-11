# Notifier
Notifier is a wrapper of the standard library HTTP client that helps your application make a large number of requests, at scale.
It can be used in as a library or from the command-line interface.

## How it works
The requests are processed in worker polls. A fixed number of `x` workers work their way through `N` requests in a work queue.
Each request stays in a queue until a worker finishes up its current task and pulls a new one off.
To increase the concurrency level each request goes through different work queues according to the request's status (sending or processing response).
The number of workers is configurable.

## Library

    // Prepare the requests.
    var requests []*http.Request  
    for _, body := range bodies {  
      req, _ := http.NewRequest(http.MethodPost, URL, bytes.NewBuffer([]byte("the body")))  
      requests = append(requests, req)  
    }  
    
    // Set the amount of workers.
    dispatchRequestsWorkers := 20
    processResponseWorkers := 20
    
    // Send the requests in bulk.  
    bulkRequest := pkg.NewBulkRequest(requests, dispatchRequestsWorkers, processResponseWorkers)  
    HTTPClient.Do(bulkRequest)

## With command-line
Run `make all` to install the dependencies, run the tests and compile the program for the main platforms.
The binaries will be created under the folder `bin`.

#### Synopsis:

    notifier [command] [flags]
    
    Commands:
    - notify
	    Reads the messages from STDIN. Each line is considered a new message.
	    
    Flags:
     -chunkSize int
        The amount of messages to process in bulk. (default 1)
     -interval duration
        The interval between each operation. (default 1s)
     -requestTimeout duration
        The timeout for each HTTP request. (default 1s)
     -url string
        The target URL that will receive the notifications. (Mandatory)

#### Default settings

    notifier-linux notify --url "https://example.com/receiver" < messages.txt

#### Custom settings
Send notification in bulk of 10 requests at a time with an interval of 500 milliseconds and a request timeout of 2 seconds:

    notifier-linux notify --url "https://example.com/receiver" --chunkSize=10  --interval=500ms requestTimeout=2s < messages.txt

#### Example output

    2020/11/11 13:03:07 Sending notifications...
    2020/11/11 13:03:07 Processing message: first
    2020/11/11 13:03:08 Processing message: second
    2020/11/11 13:03:08 Processing message: third
    2020/11/11 13:03:09 Processing message: fourth
    2020/11/11 13:03:09 Processing message: fifth
    2020/11/11 13:03:10 Processing message: sixth
    
    RESULTS ...
    Message at line 0 - Returned status code 404
    Message at line 1 - Returned status code 200
    Message at line 2 - Returned status code 500
    Message at line 3 - Returned status code 200
    Message at line 4 - Returned status code 0 - Error: http client error: Post "https://example.com/receiver": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
    Message at line 5 - Returned status code 404

## External libraries   
 - Test suite: https://github.com/stretchr/testify  
