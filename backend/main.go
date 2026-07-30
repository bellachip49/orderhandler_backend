package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "time/tzdata"
)

const defaultPort = 8080
const defaultHost = "127.0.0.1"

// Orders are always timestamped in Pacific time, independent of the host or
// container's local timezone. The blank "time/tzdata" import embeds the IANA
// database in the binary so this resolves correctly even on minimal images
// (e.g. Alpine) that don't ship /usr/share/zoneinfo.
var pacificLocation = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		log.Fatalf("failed to load Pacific timezone: %v", err)
	}
	return loc
}()

func main() {
	port, err := portFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:              hostFromEnvironment() + ":" + strconv.Itoa(port),
		Handler:           loggingMiddleware(newHandler()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("server listening on http://%s", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case signal := <-stop:
		log.Printf("received %s, shutting down", signal)
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			log.Fatalf("server shutdown failed: %v", err)
		}
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}
}

func portFromEnvironment() (int, error) {
	value := os.Getenv("PORT")
	if value == "" {
		return defaultPort, nil
	}

	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("PORT must be a number between 1 and 65535")
	}
	return port, nil
}

func hostFromEnvironment() string {
	value := os.Getenv("HOST")
	if value == "" {
		return defaultHost
	}
	return value
}

func newHandler() http.Handler {
	store := &orderStore{orders: make([]Order, 0)}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/health" {
			healthHandler(w, r)
			return
		}
		if r.URL.Path == "/orders" {
			ordersHandler(w, r, store)
			return
		}
		notFoundHandler(w, r)
	})
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func ordersHandler(w http.ResponseWriter, r *http.Request, store *orderStore) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, store.all())
	case http.MethodPost:
		createOrderHandler(w, r, store)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func createOrderHandler(w http.ResponseWriter, r *http.Request, store *orderStore) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var order Order
	if err := decoder.Decode(&order); err != nil {
		badRequest(w, "request body must be a valid order")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		badRequest(w, "request body must contain one order")
		return
	}
	if err := order.validate(); err != nil {
		badRequest(w, err.Error())
		return
	}

	createdOrder := store.add(order)
	writeJSON(w, http.StatusCreated, createdOrder)
}

func badRequest(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
}

func notFoundHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

type OrderItem struct {
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Quantity int     `json:"quantity"`
}

type Order struct {
	ID        int64       `json:"id"`
	CreatedAt time.Time   `json:"created_at"`
	Items     []OrderItem `json:"items"`
	Total     float64     `json:"total"`
}

func (o Order) validate() error {
	if len(o.Items) == 0 {
		return errors.New("items must contain at least one item")
	}
	if o.Total < 0 {
		return errors.New("total must not be negative")
	}
	for _, item := range o.Items {
		if strings.TrimSpace(item.Name) == "" {
			return errors.New("each item must have a name")
		}
		if item.Price < 0 {
			return errors.New("item price must not be negative")
		}
		if item.Quantity < 1 {
			return errors.New("item quantity must be at least 1")
		}
	}
	return nil
}

type orderStore struct {
	mu     sync.RWMutex
	nextID int64
	orders []Order
}

func (s *orderStore) add(order Order) Order {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	order.ID = s.nextID
	order.CreatedAt = time.Now().In(pacificLocation)
	s.orders = append(s.orders, order)
	return order
}

func (s *orderStore) all() []Order {
	s.mu.RLock()
	defer s.mu.RUnlock()

	orders := make([]Order, len(s.orders))
	copy(orders, s.orders)
	return orders
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, recorder.status, time.Since(started).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
