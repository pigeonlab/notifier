package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"github.com/pigeonlab/notifier/pkg"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// configuration handle this program's configuration
type configuration struct {
	targetUrl      string
	chunkSize      int
	interval       time.Duration
	requestTimeout time.Duration
}

// result represents the program's output
type result struct {
	responses []*http.Response
	errors    []error
}

func main() {
	mainCommand := flag.NewFlagSet("notify", flag.ExitOnError)
	targetURL := mainCommand.String("url", "", "The target URL that will receive the notifications.")
	chunkSize := mainCommand.Int("chunkSize", 1, "The amount of messages to process in bulk.")
	interval := mainCommand.Duration("interval", 1*time.Second, "The interval between each operation.")
	requestTimeout := mainCommand.Duration("requestTimeout", 1*time.Second, "The timeout for each HTTP request.")

	// Enforce the right number of command and flags.
	if len(os.Args) < 2 {
		log.Println(`You must specify a command. Commands available: "notify"`)
		os.Exit(1)
	}

	// Make sure the command exists.
	switch os.Args[1] {
	case "notify":
		err := mainCommand.Parse(os.Args[2:])
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}
	default:
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Mandatory flags to check.
	if mainCommand.Parsed() {
		if *targetURL == "" {
			log.Println("The --url flag is mandatory.")
			mainCommand.PrintDefaults()
			os.Exit(1)
		}
	}

	// Validate URL.
	_, err := url.ParseRequestURI(*targetURL)
	if err != nil {
		log.Println("The --url value is invalid.")
		mainCommand.PrintDefaults()
		os.Exit(1)
	}

	// Setup configuration
	conf := configuration{
		targetUrl:      *targetURL,
		chunkSize:      *chunkSize,
		interval:       *interval,
		requestTimeout: *requestTimeout,
	}

	// Listen for OS interrupt signals.
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)

	// Create a cancellable context.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Wait for a signal.
		OSCall := <-c
		log.Printf("The program received a system call: %+v.\n", OSCall)

		// Cancel the context.
		cancel()
	}()

	// Prepare HTTP client and inject the cancellable context.
	HTTPClient := &http.Client{Timeout: *requestTimeout}
	bulkHTTPClient := pkg.NewBulkHTTPClient(HTTPClient, ctx)

	// Start the program has child process.
	ticker := time.NewTicker(*interval)
	go startProgram(conf, ticker, bulkHTTPClient, cancel)

	log.Println("Sending notifications...")
	<-ctx.Done()
	log.Println("The program terminated gracefully.")
}

// startProgram starts to process the messages in STDIN.
// It cancels the context as soon as the end of input is reached
// or a fatal error is thrown.
func startProgram(
	conf configuration,
	ticker *time.Ticker,
	HTTPClient *pkg.BulkHTTPClient,
	cancel context.CancelFunc,
) {
	var finalResult result
	stdioReader := bufio.NewReader(os.Stdin)
	for range ticker.C {
		EOF, res, err := processLines(conf, stdioReader, HTTPClient)
		if err != nil {
			log.Printf("A fatal error occurred: %v", err)
			cancel()
			return
		}
		finalResult.responses = append(finalResult.responses, res.responses...)
		finalResult.errors = append(finalResult.errors, res.errors...)

		if EOF {
			printResult(finalResult)
			cancel()
			return
		}
	}
}

// processLines processes multiple notifications at a time according to the limit.
func processLines(
	conf configuration,
	reader *bufio.Reader,
	HTTPClient *pkg.BulkHTTPClient,
) (EOF bool, res result, err error) {
	var messages []string
	var errs []error
	var responses []*http.Response

	for i := 0; i < conf.chunkSize; i++ {
		text, err := reader.ReadString('\n')
		switch err {
		case nil:
			log.Printf("Processing message: %s", text)
		case io.EOF:
			log.Printf("Processing message: %s", text)
			EOF = true
			break
		default:
			return false, result{}, err
		}

		messages = append(messages, text)
	}

	if len(messages) > 0 {
		responses, errs = sendNotifications(HTTPClient, conf.targetUrl, messages)
	}

	return EOF, result{
		responses: responses,
		errors:    errs,
	}, nil
}

// sendNotifications sends a bulk request.
// It gathers all the request bodies in a single bulk request.
func sendNotifications(HTTPClient *pkg.BulkHTTPClient, URL string, bodies []string) ([]*http.Response, []error) {
	var requests []*http.Request
	for _, body := range bodies {
		req, _ := http.NewRequest(http.MethodPost, URL, bytes.NewBuffer([]byte(body)))
		requests = append(requests, req)
	}

	// Send request with a default amount of workers set to 20
	bulkRequest := pkg.NewBulkRequest(requests, 20, 20)
	return HTTPClient.Do(bulkRequest)
}

// printResult pretty prints the output before exiting.
func printResult(finalResult result) {
	fmt.Print("\nRESULTS ...\n")
	for i := 0; i < len(finalResult.responses); i++ {
		statusCode := 0
		if finalResult.responses[i] != nil {
			statusCode = finalResult.responses[i].StatusCode
		}
		if finalResult.errors[i] != nil {
			fmt.Printf("Message at line %d - Returned status code %d - Error: %v\n", i, statusCode, finalResult.errors[i])
		} else {
			fmt.Printf("Message at line %d - Returned status code %d\n", i, statusCode)
		}
	}
}
