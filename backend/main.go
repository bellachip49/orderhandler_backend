package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
	_ "time/tzdata"
)

const defaultPort = 8080
const defaultHost = "0.0.0.0"
const defaultDBPath = "orders.db"

const createOrdersTableSQL = `
CREATE TABLE IF NOT EXISTS orders (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at TEXT NOT NULL,
	items      TEXT NOT NULL,
	total      REAL NOT NULL
)`

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

	db, err := openDatabase(dbPathFromEnvironment())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	server := &http.Server{
		Addr:              hostFromEnvironment() + ":" + strconv.Itoa(port),
		Handler:           loggingMiddleware(newHandler(db)),
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

func dbPathFromEnvironment() string {
	value := os.Getenv("DB_PATH")
	if value == "" {
		return defaultDBPath
	}
	return value
}

func openDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(createOrdersTableSQL); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func newHandler(db *sql.DB) http.Handler {
	store := &orderStore{db: db}

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
		orders, err := store.all()
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, orders)
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

	createdOrder, err := store.add(order)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createdOrder)
}

func badRequest(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
}

func internalError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
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
	db *sql.DB
}

func (s *orderStore) add(order Order) (Order, error) {
	itemsJSON, err := json.Marshal(order.Items)
	if err != nil {
		return Order{}, err
	}
	order.CreatedAt = time.Now().In(pacificLocation)

	result, err := s.db.Exec(
		"INSERT INTO orders (created_at, items, total) VALUES (?, ?, ?)",
		order.CreatedAt.Format(time.RFC3339), string(itemsJSON), order.Total,
	)
	if err != nil {
		return Order{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Order{}, err
	}
	order.ID = id
	return order, nil
}

func (s *orderStore) all() ([]Order, error) {
	rows, err := s.db.Query("SELECT id, created_at, items, total FROM orders ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := make([]Order, 0)
	for rows.Next() {
		var (
			order     Order
			createdAt string
			itemsJSON string
		)
		if err := rows.Scan(&order.ID, &createdAt, &itemsJSON, &order.Total); err != nil {
			return nil, err
		}
		order.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(itemsJSON), &order.Items); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, nil
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
