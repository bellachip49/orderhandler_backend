package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	_ "modernc.org/sqlite"
	_ "time/tzdata"
)

const defaultPort = 8080
const defaultHost = "0.0.0.0"
const defaultDBPath = "orders.db"
const defaultPrinterPort = 9100
const startingOrderNumber = 1000
const defaultBasicAuthUsername = "admin"
const defaultBasicAuthPassword = "orderbackend"
const defaultPrinterTextEncoding = printerTextEncodingUTF8

type printerTextEncoding string

const (
	printerTextEncodingUTF8 printerTextEncoding = "utf8"
	printerTextEncodingGBK  printerTextEncoding = "gbk"
)

var pacificLocation = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		log.Fatalf("failed to load Pacific timezone: %v", err)
	}
	return loc
}()

type app struct {
	store   *store
	printer PrinterService
	auth    basicAuthConfig
}

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

	textEncoding, err := printerTextEncodingFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}

	application := &app{
		store:   &store{db: db},
		printer: TCPPrinterService{Timeout: 8 * time.Second, TextEncoding: textEncoding},
		auth:    basicAuthFromEnvironment(),
	}

	server := &http.Server{
		Addr:              hostFromEnvironment() + ":" + strconv.Itoa(port),
		Handler:           loggingMiddleware(newHandler(application)),
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

func basicAuthFromEnvironment() basicAuthConfig {
	username := os.Getenv("BASIC_AUTH_USERNAME")
	if username == "" {
		username = defaultBasicAuthUsername
	}
	password := os.Getenv("BASIC_AUTH_PASSWORD")
	if password == "" {
		password = defaultBasicAuthPassword
	}
	return basicAuthConfig{Username: username, Password: password}
}

func printerTextEncodingFromEnvironment() (printerTextEncoding, error) {
	return parsePrinterTextEncoding(os.Getenv("PRINTER_TEXT_ENCODING"))
}

func parsePrinterTextEncoding(value string) (printerTextEncoding, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(printerTextEncodingUTF8):
		return printerTextEncodingUTF8, nil
	case string(printerTextEncodingGBK):
		return printerTextEncodingGBK, nil
	default:
		return "", errors.New("PRINTER_TEXT_ENCODING must be utf8 or gbk")
	}
}

func openDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateLegacySchema(db); err != nil {
		db.Close()
		return nil, err
	}
	for _, statement := range schemaStatements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := ensureSaleItemDescriptionColumn(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureOrderItemDescriptionColumn(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := seedDefaults(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func ensureSaleItemDescriptionColumn(db *sql.DB) error {
	return ensureColumn(db, "sale_items", "description", `ALTER TABLE sale_items ADD COLUMN description TEXT NOT NULL DEFAULT ''`)
}

func ensureOrderItemDescriptionColumn(db *sql.DB) error {
	return ensureColumn(db, "order_items", "description", `ALTER TABLE order_items ADD COLUMN description TEXT NOT NULL DEFAULT ''`)
}

func ensureColumn(db *sql.DB, tableName string, columnName string, alterStatement string) error {
	rows, err := db.Query(`PRAGMA table_info(` + tableName + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == columnName {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Exec(alterStatement)
	return err
}

func migrateLegacySchema(db *sql.DB) error {
	var tableName string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'orders'`).Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	rows, err := db.Query(`PRAGMA table_info(orders)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasOrderNumber := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "order_number" {
			hasOrderNumber = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasOrderNumber {
		return nil
	}

	legacyName := "legacy_orders_v0"
	var existing int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, legacyName).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		legacyName = "legacy_orders_v0_" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	_, err = db.Exec(`ALTER TABLE orders RENAME TO ` + legacyName)
	return err
}

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS sale_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		price_cents INTEGER NOT NULL CHECK(price_cents >= 0),
		active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0, 1)),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS printers (
		role TEXT PRIMARY KEY CHECK(role IN ('cashier', 'kitchen')),
		display_name TEXT NOT NULL,
		host TEXT NOT NULL DEFAULT '',
		port INTEGER NOT NULL DEFAULT 9100 CHECK(port BETWEEN 1 AND 65535),
		enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0, 1)),
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS orders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		order_number TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		total_cents INTEGER NOT NULL CHECK(total_cents >= 0)
	)`,
	`CREATE TABLE IF NOT EXISTS order_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		order_id INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
		sale_item_id INTEGER,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		unit_price_cents INTEGER NOT NULL CHECK(unit_price_cents >= 0),
		quantity INTEGER NOT NULL CHECK(quantity > 0),
		subtotal_cents INTEGER NOT NULL CHECK(subtotal_cents >= 0)
	)`,
	`CREATE TABLE IF NOT EXISTS print_jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		order_id INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
		printer_role TEXT NOT NULL CHECK(printer_role IN ('cashier', 'kitchen')),
		status TEXT NOT NULL CHECK(status IN ('pending', 'sent', 'skipped', 'failed')),
		attempt_count INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
}

func seedDefaults(db *sql.DB) error {
	now := nowString()
	for _, printer := range []Printer{
		{Role: "cashier", DisplayName: "Cashier Printer", Port: defaultPrinterPort},
		{Role: "kitchen", DisplayName: "Kitchen Printer", Port: defaultPrinterPort},
	} {
		if _, err := db.Exec(
			`INSERT OR IGNORE INTO printers (role, display_name, host, port, enabled, updated_at) VALUES (?, ?, '', ?, 0, ?)`,
			printer.Role, printer.DisplayName, printer.Port, now,
		); err != nil {
			return err
		}
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sale_items").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	defaults := []SaleItem{
		{Name: "Dumplings", Description: "Item description placeholder", PriceCents: 600, Active: true},
		{Name: "Cart Noodles", Description: "Item description placeholder", PriceCents: 800, Active: true},
		{Name: "Fish balls", Description: "Item description placeholder", PriceCents: 500, Active: true},
		{Name: "Milk Tea", Description: "Item description placeholder", PriceCents: 450, Active: true},
		{Name: "Potato Chips", Description: "Item description placeholder", PriceCents: 250, Active: true},
	}
	for _, item := range defaults {
		if _, err := db.Exec(
			`INSERT INTO sale_items (name, description, price_cents, active, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)`,
			item.Name, item.Description, item.PriceCents, now, now,
		); err != nil {
			return err
		}
	}
	return nil
}

func newHandler(application *app) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" && !application.auth.authorized(r) {
			logFailedAuthentication(r)
			unauthorized(w)
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		case r.URL.Path == "/orders":
			legacyOrdersHandler(w, r, application.store)
		case r.URL.Path == "/api/v1/catalog":
			catalogHandler(w, r, application.store)
		case r.URL.Path == "/api/v1/sale-items":
			saleItemsHandler(w, r, application.store)
		case strings.HasPrefix(r.URL.Path, "/api/v1/sale-items/"):
			saleItemHandler(w, r, application.store)
		case r.URL.Path == "/api/v1/printers":
			printersHandler(w, r, application.store)
		case strings.HasPrefix(r.URL.Path, "/api/v1/printers/"):
			printerHandler(w, r, application.store)
		case r.URL.Path == "/api/v1/order-summary/print":
			orderSummaryPrintHandler(w, r, application)
		case r.URL.Path == "/api/v1/order-summary":
			orderSummaryHandler(w, r, application.store)
		case r.URL.Path == "/api/v1/orders":
			ordersHandler(w, r, application)
		case strings.HasPrefix(r.URL.Path, "/api/v1/orders/"):
			orderHandler(w, r, application.store)
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
	})
}

func logFailedAuthentication(r *http.Request) {
	username, _, ok := r.BasicAuth()
	if !ok || username == "" {
		username = "<missing>"
	}
	log.Printf(
		"authentication failed: method=%s path=%s remote_addr=%s username=%q",
		r.Method,
		r.URL.Path,
		r.RemoteAddr,
		username,
	)
}

type basicAuthConfig struct {
	Username string
	Password string
}

func (c basicAuthConfig) authorized(r *http.Request) bool {
	username, password, ok := r.BasicAuth()
	if !ok {
		return false
	}
	usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(c.Username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(password), []byte(c.Password)) == 1
	return usernameMatch && passwordMatch
}

func catalogHandler(w http.ResponseWriter, r *http.Request, store *store) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	items, err := store.listSaleItems(true)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func saleItemsHandler(w http.ResponseWriter, r *http.Request, store *store) {
	switch r.Method {
	case http.MethodGet:
		items, err := store.listSaleItems(false)
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var request saleItemRequest
		if !decodeBody(w, r, &request) {
			return
		}
		item, err := request.toSaleItem()
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		created, err := store.createSaleItem(item)
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func saleItemHandler(w http.ResponseWriter, r *http.Request, store *store) {
	id, ok := parseIDFromPath(w, r.URL.Path, "/api/v1/sale-items/")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPut:
		var request saleItemRequest
		if !decodeBody(w, r, &request) {
			return
		}
		item, err := request.toSaleItem()
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		item.ID = id
		updated, err := store.updateSaleItem(item)
		if errors.Is(err, errNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "sale item not found"})
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := store.setSaleItemActive(id, false); errors.Is(err, errNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "sale item not found"})
		} else if err != nil {
			internalError(w, err)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		methodNotAllowed(w, "PUT, DELETE")
	}
}

func printersHandler(w http.ResponseWriter, r *http.Request, store *store) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	printers, err := store.listPrinters()
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, printers)
}

func printerHandler(w http.ResponseWriter, r *http.Request, store *store) {
	role := strings.TrimPrefix(r.URL.Path, "/api/v1/printers/")
	if role != "cashier" && role != "kitchen" {
		badRequest(w, "printer role must be cashier or kitchen")
		return
	}
	switch r.Method {
	case http.MethodGet:
		printer, err := store.getPrinter(role)
		if errors.Is(err, errNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "printer not found"})
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, printer)
	case http.MethodPut:
		var request printerRequest
		if !decodeBody(w, r, &request) {
			return
		}
		printer, err := request.toPrinter(role)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		updated, err := store.updatePrinter(printer)
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	default:
		methodNotAllowed(w, "GET, PUT")
	}
}

func ordersHandler(w http.ResponseWriter, r *http.Request, application *app) {
	switch r.Method {
	case http.MethodGet:
		orders, err := application.store.listOrders()
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, orders)
	case http.MethodPost:
		var request createOrderRequest
		if !decodeBody(w, r, &request) {
			return
		}
		order, jobs, err := application.store.createOrder(request)
		if errors.Is(err, errValidation) {
			badRequest(w, err.Error())
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		go application.dispatchPrintJobs(order.ID, jobs)
		writeJSON(w, http.StatusCreated, order)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func orderHandler(w http.ResponseWriter, r *http.Request, store *store) {
	id, ok := parseIDFromPath(w, r.URL.Path, "/api/v1/orders/")
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	order, err := store.getOrder(id)
	if errors.Is(err, errNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found"})
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func orderSummaryHandler(w http.ResponseWriter, r *http.Request, store *store) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	summary, err := store.getOrderSummary()
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func orderSummaryPrintHandler(w http.ResponseWriter, r *http.Request, application *app) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}

	summary, err := application.store.getOrderSummary()
	if err != nil {
		internalError(w, err)
		return
	}
	printer, err := application.store.getPrinter("cashier")
	if errors.Is(err, errNotFound) {
		badRequest(w, "cashier printer is not configured")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	if !printer.Enabled {
		badRequest(w, "cashier printer is disabled")
		return
	}
	if strings.TrimSpace(printer.Host) == "" {
		badRequest(w, "cashier printer is not configured")
		return
	}
	if printer.Port < 1 || printer.Port > 65535 {
		badRequest(w, "cashier printer port must be between 1 and 65535")
		return
	}

	if err := application.printer.PrintSummary(printer, summary); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, PrintOrderSummaryResponse{
		PrinterRole: printer.Role,
		Summary:     summary,
	})
}

func legacyOrdersHandler(w http.ResponseWriter, r *http.Request, store *store) {
	switch r.Method {
	case http.MethodGet:
		orders, err := store.listOrders()
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, orders)
	case http.MethodPost:
		var request legacyOrderRequest
		if !decodeBody(w, r, &request) {
			return
		}
		order, err := store.createLegacyOrder(request)
		if errors.Is(err, errValidation) {
			badRequest(w, err.Error())
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, order)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (a *app) dispatchPrintJobs(orderID int64, jobs []PrintJob) {
	order, err := a.store.getOrder(orderID)
	if err != nil {
		log.Printf("load order for printing: %v", err)
		return
	}
	printers, err := a.store.listPrinters()
	if err != nil {
		log.Printf("load printers for printing: %v", err)
		return
	}
	printersByRole := make(map[string]Printer, len(printers))
	for _, printer := range printers {
		printersByRole[printer.Role] = printer
	}
	var disabledPrinters []string
	for _, job := range jobs {
		printer, ok := printersByRole[job.PrinterRole]
		if !ok {
			if err := a.store.updatePrintJob(job.ID, "skipped", "printer is not configured"); err != nil {
				log.Printf("mark print job skipped: %v", err)
			}
			continue
		}
		if !printer.Enabled {
			disabledPrinters = append(disabledPrinters, printer.Role)
			if err := a.store.updatePrintJob(job.ID, "skipped", "printer is disabled"); err != nil {
				log.Printf("mark print job skipped: %v", err)
			}
			continue
		}
		if strings.TrimSpace(printer.Host) == "" {
			if err := a.store.updatePrintJob(job.ID, "skipped", "printer is not configured"); err != nil {
				log.Printf("mark print job skipped: %v", err)
			}
			continue
		}
		if err := a.printer.Print(printer, order); err != nil {
			if updateErr := a.store.updatePrintJob(job.ID, "failed", err.Error()); updateErr != nil {
				log.Printf("mark print job failed: %v", updateErr)
			}
			continue
		}
		if err := a.store.updatePrintJob(job.ID, "sent", ""); err != nil {
			log.Printf("mark print job sent: %v", err)
		}
	}
	if len(disabledPrinters) > 0 {
		log.Printf("disabled printers for order #%s: %s", order.OrderNumber, strings.Join(disabledPrinters, ", "))
	}
}

type store struct {
	db *sql.DB
}

var errNotFound = errors.New("not found")
var errValidation = errors.New("validation failed")

type SaleItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	PriceCents  int    `json:"price_cents"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type Printer struct {
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Enabled     bool   `json:"enabled"`
	UpdatedAt   string `json:"updated_at"`
}

type Order struct {
	ID          int64       `json:"id"`
	OrderNumber string      `json:"order_number"`
	CreatedAt   string      `json:"created_at"`
	Items       []OrderItem `json:"items"`
	TotalCents  int         `json:"total_cents"`
	PrintJobs   []PrintJob  `json:"print_jobs,omitempty"`
}

type OrderSummary struct {
	OrderCount int                `json:"order_count"`
	TotalCents int                `json:"total_cents"`
	Items      []OrderSummaryItem `json:"items"`
}

type OrderSummaryItem struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	QuantitySold int    `json:"quantity_sold"`
	TotalCents   int    `json:"total_cents"`
}

type PrintOrderSummaryResponse struct {
	PrinterRole string       `json:"printer_role"`
	Summary     OrderSummary `json:"summary"`
}

type OrderItem struct {
	ID             int64  `json:"id,omitempty"`
	SaleItemID     int64  `json:"sale_item_id,omitempty"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	UnitPriceCents int    `json:"unit_price_cents"`
	Quantity       int    `json:"quantity"`
	SubtotalCents  int    `json:"subtotal_cents"`
}

type PrintJob struct {
	ID           int64  `json:"id"`
	OrderID      int64  `json:"order_id"`
	PrinterRole  string `json:"printer_role"`
	Status       string `json:"status"`
	AttemptCount int    `json:"attempt_count"`
	LastError    string `json:"last_error"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type saleItemRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	PriceCents  int    `json:"price_cents"`
	Active      bool   `json:"active"`
}

func (r saleItemRequest) toSaleItem() (SaleItem, error) {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return SaleItem{}, errors.New("name is required")
	}
	if r.PriceCents < 0 {
		return SaleItem{}, errors.New("price_cents must not be negative")
	}
	return SaleItem{
		Name:        name,
		Description: strings.TrimSpace(r.Description),
		PriceCents:  r.PriceCents,
		Active:      r.Active,
	}, nil
}

type printerRequest struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Enabled bool   `json:"enabled"`
}

func (r printerRequest) toPrinter(role string) (Printer, error) {
	port := r.Port
	if port == 0 {
		port = defaultPrinterPort
	}
	if port < 1 || port > 65535 {
		return Printer{}, errors.New("port must be between 1 and 65535")
	}
	return Printer{
		Role:        role,
		DisplayName: printerDisplayName(role),
		Host:        strings.TrimSpace(r.Host),
		Port:        port,
		Enabled:     r.Enabled,
	}, nil
}

type createOrderRequest struct {
	Items []createOrderItemRequest `json:"items"`
}

type createOrderItemRequest struct {
	SaleItemID int64 `json:"sale_item_id"`
	Quantity   int   `json:"quantity"`
}

type legacyOrderRequest struct {
	Items []struct {
		Name     string  `json:"name"`
		Price    float64 `json:"price"`
		Quantity int     `json:"quantity"`
	} `json:"items"`
	Total float64 `json:"total"`
}

func (s *store) listSaleItems(activeOnly bool) ([]SaleItem, error) {
	query := "SELECT id, name, description, price_cents, active, created_at, updated_at FROM sale_items"
	if activeOnly {
		query += " WHERE active = 1"
	}
	query += " ORDER BY name COLLATE NOCASE"
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SaleItem
	for rows.Next() {
		item, err := scanSaleItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanSaleItem(rows interface {
	Scan(dest ...any) error
}) (SaleItem, error) {
	var item SaleItem
	var active int
	if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.PriceCents, &active, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return SaleItem{}, err
	}
	item.Active = active == 1
	return item, nil
}

func (s *store) createSaleItem(item SaleItem) (SaleItem, error) {
	now := nowString()
	result, err := s.db.Exec(
		`INSERT INTO sale_items (name, description, price_cents, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		item.Name, item.Description, item.PriceCents, boolInt(item.Active), now, now,
	)
	if err != nil {
		return SaleItem{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return SaleItem{}, err
	}
	item.ID = id
	item.CreatedAt = now
	item.UpdatedAt = now
	return item, nil
}

func (s *store) updateSaleItem(item SaleItem) (SaleItem, error) {
	now := nowString()
	result, err := s.db.Exec(
		`UPDATE sale_items SET name = ?, description = ?, price_cents = ?, active = ?, updated_at = ? WHERE id = ?`,
		item.Name, item.Description, item.PriceCents, boolInt(item.Active), now, item.ID,
	)
	if err != nil {
		return SaleItem{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return SaleItem{}, err
	}
	if rows == 0 {
		return SaleItem{}, errNotFound
	}
	return s.getSaleItem(item.ID)
}

func (s *store) setSaleItemActive(id int64, active bool) error {
	result, err := s.db.Exec(`UPDATE sale_items SET active = ?, updated_at = ? WHERE id = ?`, boolInt(active), nowString(), id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errNotFound
	}
	return nil
}

func (s *store) getSaleItem(id int64) (SaleItem, error) {
	row := s.db.QueryRow(`SELECT id, name, description, price_cents, active, created_at, updated_at FROM sale_items WHERE id = ?`, id)
	item, err := scanSaleItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SaleItem{}, errNotFound
	}
	return item, err
}

func (s *store) listPrinters() ([]Printer, error) {
	rows, err := s.db.Query(`SELECT role, display_name, host, port, enabled, updated_at FROM printers ORDER BY role`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var printers []Printer
	for rows.Next() {
		printer, err := scanPrinter(rows)
		if err != nil {
			return nil, err
		}
		printers = append(printers, printer)
	}
	return printers, rows.Err()
}

func (s *store) getPrinter(role string) (Printer, error) {
	row := s.db.QueryRow(`SELECT role, display_name, host, port, enabled, updated_at FROM printers WHERE role = ?`, role)
	printer, err := scanPrinter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Printer{}, errNotFound
	}
	return printer, err
}

func scanPrinter(rows interface {
	Scan(dest ...any) error
}) (Printer, error) {
	var printer Printer
	var enabled int
	if err := rows.Scan(&printer.Role, &printer.DisplayName, &printer.Host, &printer.Port, &enabled, &printer.UpdatedAt); err != nil {
		return Printer{}, err
	}
	printer.Enabled = enabled == 1
	return printer, nil
}

func (s *store) updatePrinter(printer Printer) (Printer, error) {
	if printer.DisplayName == "" {
		printer.DisplayName = printerDisplayName(printer.Role)
	}
	now := nowString()
	_, err := s.db.Exec(
		`INSERT INTO printers (role, display_name, host, port, enabled, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(role) DO UPDATE SET host = excluded.host, port = excluded.port, enabled = excluded.enabled, updated_at = excluded.updated_at`,
		printer.Role, printer.DisplayName, printer.Host, printer.Port, boolInt(printer.Enabled), now,
	)
	if err != nil {
		return Printer{}, err
	}
	return s.getPrinter(printer.Role)
}

func (s *store) createOrder(request createOrderRequest) (Order, []PrintJob, error) {
	if len(request.Items) == 0 {
		return Order{}, nil, fmt.Errorf("%w: items must contain at least one item", errValidation)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Order{}, nil, err
	}
	defer tx.Rollback()

	var orderItems []OrderItem
	itemIDs := make(map[int64]bool)
	total := 0
	for _, requestItem := range request.Items {
		if requestItem.SaleItemID <= 0 {
			return Order{}, nil, fmt.Errorf("%w: each item must include sale_item_id", errValidation)
		}
		if requestItem.Quantity < 1 {
			return Order{}, nil, fmt.Errorf("%w: quantity must be at least 1", errValidation)
		}
		if itemIDs[requestItem.SaleItemID] {
			return Order{}, nil, fmt.Errorf("%w: duplicate sale_item_id %d", errValidation, requestItem.SaleItemID)
		}
		itemIDs[requestItem.SaleItemID] = true

		var item SaleItem
		var active int
		err := tx.QueryRow(
			`SELECT id, name, description, price_cents, active, created_at, updated_at FROM sale_items WHERE id = ?`,
			requestItem.SaleItemID,
		).Scan(&item.ID, &item.Name, &item.Description, &item.PriceCents, &active, &item.CreatedAt, &item.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return Order{}, nil, fmt.Errorf("%w: sale item %d was not found", errValidation, requestItem.SaleItemID)
		}
		if err != nil {
			return Order{}, nil, err
		}
		if active != 1 {
			return Order{}, nil, fmt.Errorf("%w: sale item %d is inactive", errValidation, requestItem.SaleItemID)
		}
		subtotal := item.PriceCents * requestItem.Quantity
		total += subtotal
		orderItems = append(orderItems, OrderItem{
			SaleItemID:     item.ID,
			Name:           item.Name,
			Description:    item.Description,
			UnitPriceCents: item.PriceCents,
			Quantity:       requestItem.Quantity,
			SubtotalCents:  subtotal,
		})
	}

	now := nowString()
	result, err := tx.Exec(`INSERT INTO orders (order_number, created_at, total_cents) VALUES ('pending', ?, ?)`, now, total)
	if err != nil {
		return Order{}, nil, err
	}
	orderID, err := result.LastInsertId()
	if err != nil {
		return Order{}, nil, err
	}
	orderNumber := strconv.FormatInt(startingOrderNumber+orderID, 10)
	if _, err := tx.Exec(`UPDATE orders SET order_number = ? WHERE id = ?`, orderNumber, orderID); err != nil {
		return Order{}, nil, err
	}
	for i := range orderItems {
		result, err := tx.Exec(
			`INSERT INTO order_items (order_id, sale_item_id, name, description, unit_price_cents, quantity, subtotal_cents) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			orderID, orderItems[i].SaleItemID, orderItems[i].Name, orderItems[i].Description, orderItems[i].UnitPriceCents, orderItems[i].Quantity, orderItems[i].SubtotalCents,
		)
		if err != nil {
			return Order{}, nil, err
		}
		orderItems[i].ID, err = result.LastInsertId()
		if err != nil {
			return Order{}, nil, err
		}
	}

	jobs := make([]PrintJob, 0, 2)
	for _, role := range []string{"cashier", "kitchen"} {
		result, err := tx.Exec(
			`INSERT INTO print_jobs (order_id, printer_role, status, created_at, updated_at) VALUES (?, ?, 'pending', ?, ?)`,
			orderID, role, now, now,
		)
		if err != nil {
			return Order{}, nil, err
		}
		jobID, err := result.LastInsertId()
		if err != nil {
			return Order{}, nil, err
		}
		jobs = append(jobs, PrintJob{ID: jobID, OrderID: orderID, PrinterRole: role, Status: "pending", CreatedAt: now, UpdatedAt: now})
	}
	if err := tx.Commit(); err != nil {
		return Order{}, nil, err
	}
	return Order{ID: orderID, OrderNumber: orderNumber, CreatedAt: now, Items: orderItems, TotalCents: total, PrintJobs: jobs}, jobs, nil
}

func (s *store) createLegacyOrder(request legacyOrderRequest) (Order, error) {
	if len(request.Items) == 0 {
		return Order{}, fmt.Errorf("%w: items must contain at least one item", errValidation)
	}
	items := make([]OrderItem, 0, len(request.Items))
	total := 0
	for _, item := range request.Items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return Order{}, fmt.Errorf("%w: each item must have a name", errValidation)
		}
		if item.Price < 0 {
			return Order{}, fmt.Errorf("%w: item price must not be negative", errValidation)
		}
		if item.Quantity < 1 {
			return Order{}, fmt.Errorf("%w: item quantity must be at least 1", errValidation)
		}
		priceCents := int(item.Price*100 + 0.5)
		subtotal := priceCents * item.Quantity
		total += subtotal
		items = append(items, OrderItem{Name: name, UnitPriceCents: priceCents, Quantity: item.Quantity, SubtotalCents: subtotal})
	}
	order, _, err := s.createOrderFromSnapshot(items, total)
	return order, err
}

func (s *store) createOrderFromSnapshot(items []OrderItem, total int) (Order, []PrintJob, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Order{}, nil, err
	}
	defer tx.Rollback()
	now := nowString()
	result, err := tx.Exec(`INSERT INTO orders (order_number, created_at, total_cents) VALUES ('pending', ?, ?)`, now, total)
	if err != nil {
		return Order{}, nil, err
	}
	orderID, err := result.LastInsertId()
	if err != nil {
		return Order{}, nil, err
	}
	orderNumber := strconv.FormatInt(startingOrderNumber+orderID, 10)
	if _, err := tx.Exec(`UPDATE orders SET order_number = ? WHERE id = ?`, orderNumber, orderID); err != nil {
		return Order{}, nil, err
	}
	for i := range items {
		result, err := tx.Exec(
			`INSERT INTO order_items (order_id, name, description, unit_price_cents, quantity, subtotal_cents) VALUES (?, ?, ?, ?, ?, ?)`,
			orderID, items[i].Name, items[i].Description, items[i].UnitPriceCents, items[i].Quantity, items[i].SubtotalCents,
		)
		if err != nil {
			return Order{}, nil, err
		}
		items[i].ID, err = result.LastInsertId()
		if err != nil {
			return Order{}, nil, err
		}
	}
	jobs := make([]PrintJob, 0, 2)
	for _, role := range []string{"cashier", "kitchen"} {
		result, err := tx.Exec(
			`INSERT INTO print_jobs (order_id, printer_role, status, created_at, updated_at) VALUES (?, ?, 'pending', ?, ?)`,
			orderID, role, now, now,
		)
		if err != nil {
			return Order{}, nil, err
		}
		jobID, err := result.LastInsertId()
		if err != nil {
			return Order{}, nil, err
		}
		jobs = append(jobs, PrintJob{ID: jobID, OrderID: orderID, PrinterRole: role, Status: "pending", CreatedAt: now, UpdatedAt: now})
	}
	if err := tx.Commit(); err != nil {
		return Order{}, nil, err
	}
	return Order{ID: orderID, OrderNumber: orderNumber, CreatedAt: now, Items: items, TotalCents: total, PrintJobs: jobs}, jobs, nil
}

func (s *store) listOrders() ([]Order, error) {
	rows, err := s.db.Query(`SELECT id, order_number, created_at, total_cents FROM orders ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var order Order
		if err := rows.Scan(&order.ID, &order.OrderNumber, &order.CreatedAt, &order.TotalCents); err != nil {
			return nil, err
		}
		order.Items, err = s.listOrderItems(order.ID)
		if err != nil {
			return nil, err
		}
		order.PrintJobs, err = s.listPrintJobs(order.ID)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

func (s *store) getOrderSummary() (OrderSummary, error) {
	summary := OrderSummary{Items: []OrderSummaryItem{}}
	err := s.db.QueryRow(`
		SELECT
			COUNT(DISTINCT order_items.order_id),
			COALESCE(SUM(order_items.subtotal_cents), 0)
		FROM order_items
		JOIN sale_items ON sale_items.id = order_items.sale_item_id
		WHERE sale_items.active = 1
	`).
		Scan(&summary.OrderCount, &summary.TotalCents)
	if err != nil {
		return OrderSummary{}, err
	}

	rows, err := s.db.Query(`
		SELECT
			order_items.name,
			order_items.description,
			COALESCE(SUM(order_items.quantity), 0) AS quantity_sold,
			COALESCE(SUM(order_items.subtotal_cents), 0) AS total_cents
		FROM order_items
		JOIN sale_items ON sale_items.id = order_items.sale_item_id
		WHERE sale_items.active = 1
		GROUP BY order_items.name, order_items.description
		ORDER BY quantity_sold DESC, order_items.name COLLATE NOCASE ASC, order_items.description COLLATE NOCASE ASC
	`)
	if err != nil {
		return OrderSummary{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var item OrderSummaryItem
		if err := rows.Scan(&item.Name, &item.Description, &item.QuantitySold, &item.TotalCents); err != nil {
			return OrderSummary{}, err
		}
		summary.Items = append(summary.Items, item)
	}
	if err := rows.Err(); err != nil {
		return OrderSummary{}, err
	}

	return summary, nil
}

func (s *store) getOrder(id int64) (Order, error) {
	var order Order
	err := s.db.QueryRow(`SELECT id, order_number, created_at, total_cents FROM orders WHERE id = ?`, id).
		Scan(&order.ID, &order.OrderNumber, &order.CreatedAt, &order.TotalCents)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, errNotFound
	}
	if err != nil {
		return Order{}, err
	}
	order.Items, err = s.listOrderItems(order.ID)
	if err != nil {
		return Order{}, err
	}
	order.PrintJobs, err = s.listPrintJobs(order.ID)
	return order, err
}

func (s *store) listOrderItems(orderID int64) ([]OrderItem, error) {
	rows, err := s.db.Query(
		`SELECT id, COALESCE(sale_item_id, 0), name, description, unit_price_cents, quantity, subtotal_cents FROM order_items WHERE order_id = ? ORDER BY id`,
		orderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []OrderItem
	for rows.Next() {
		var item OrderItem
		if err := rows.Scan(&item.ID, &item.SaleItemID, &item.Name, &item.Description, &item.UnitPriceCents, &item.Quantity, &item.SubtotalCents); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *store) listPrintJobs(orderID int64) ([]PrintJob, error) {
	rows, err := s.db.Query(
		`SELECT id, order_id, printer_role, status, attempt_count, last_error, created_at, updated_at FROM print_jobs WHERE order_id = ? ORDER BY id`,
		orderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []PrintJob
	for rows.Next() {
		var job PrintJob
		if err := rows.Scan(&job.ID, &job.OrderID, &job.PrinterRole, &job.Status, &job.AttemptCount, &job.LastError, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *store) updatePrintJob(id int64, status string, lastError string) error {
	_, err := s.db.Exec(
		`UPDATE print_jobs SET status = ?, last_error = ?, attempt_count = attempt_count + 1, updated_at = ? WHERE id = ?`,
		status, lastError, nowString(), id,
	)
	return err
}

type PrinterService interface {
	Print(printer Printer, order Order) error
	PrintSummary(printer Printer, summary OrderSummary) error
}

type TCPPrinterService struct {
	Timeout      time.Duration
	TextEncoding printerTextEncoding
	Dial         func(network string, address string, timeout time.Duration) (net.Conn, error)
}

func (s TCPPrinterService) Print(printer Printer, order Order) error {
	return s.printPayload(printer, renderTicketWithEncoding(printer.Role, order, s.textEncoding()))
}

func (s TCPPrinterService) PrintSummary(printer Printer, summary OrderSummary) error {
	return s.printPayload(printer, salesSummaryTicketWithEncoding(summary, time.Now(), s.textEncoding()))
}

func (s TCPPrinterService) printPayload(printer Printer, payload []byte) error {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 8 * time.Second
	}
	dial := s.Dial
	if dial == nil {
		dial = net.DialTimeout
	}

	address := net.JoinHostPort(printer.Host, strconv.Itoa(printer.Port))
	connection, err := dial("tcp", address, timeout)
	if err != nil {
		return err
	}
	defer connection.Close()

	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	written, err := connection.Write(payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}

func (s TCPPrinterService) textEncoding() printerTextEncoding {
	if s.TextEncoding == "" {
		return defaultPrinterTextEncoding
	}
	return s.TextEncoding
}

func renderTicket(role string, order Order) []byte {
	return renderTicketWithEncoding(role, order, defaultPrinterTextEncoding)
}

func renderTicketWithEncoding(role string, order Order, encoding printerTextEncoding) []byte {
	if role == "kitchen" {
		return kitchenTicketWithEncoding(order, encoding)
	}
	return cashierTicketWithEncoding(order, encoding)
}

const receiptWidth = 30

const (
	receiptTextSizeNormal = 0x00
	receiptTextSizeBody   = 0x01
)

func cashierTicket(order Order) []byte {
	return cashierTicketWithEncoding(order, defaultPrinterTextEncoding)
}

func cashierTicketWithEncoding(order Order, encoding printerTextEncoding) []byte {
	buffer := newReceiptBuffer(encoding)
	buffer.command(0x1B, 0x40)
	buffer.command(0x1B, 0x61, 0x00)
	buffer.startText()
	buffer.textSize(receiptTextSizeBody)
	buffer.text("Order #")
	buffer.text(order.OrderNumber)
	buffer.text("\n")
	buffer.textSize(receiptTextSizeBody)
	buffer.text(divider(receiptWidth))
	buffer.text("\n")
	buffer.text(formatReceiptDate(order.CreatedAt))
	buffer.text("\n\n")
	for _, item := range order.Items {
		buffer.text(cashierItemLine(item))
		buffer.text("\n")
		if description := descriptionLine(item.Description); description != "" {
			buffer.text(description)
			buffer.text("\n")
		}
		buffer.text("\n") // Add an extra newline after each item for spacing
	}
	buffer.text("\n")
	buffer.text(divider(receiptWidth))
	buffer.text("\n")
	buffer.text(totalLine(order.TotalCents))
	buffer.text("\n\n")
	buffer.text("Thank you!\n")
	buffer.text(divider(receiptWidth))
	buffer.text("\n\n\n\n\n\n")
	buffer.textSize(receiptTextSizeNormal)
	buffer.endText()
	buffer.command(0x1D, 0x56, 0x00)
	return buffer.bytes()
}

func kitchenTicket(order Order) []byte {
	return kitchenTicketWithEncoding(order, defaultPrinterTextEncoding)
}

func kitchenTicketWithEncoding(order Order, encoding printerTextEncoding) []byte {
	buffer := newReceiptBuffer(encoding)
	buffer.command(0x1B, 0x40)
	buffer.command(0x1B, 0x61, 0x00)
	buffer.startText()
	buffer.textSize(receiptTextSizeBody)
	buffer.command(0x1B, 0x45, 0x01)
	buffer.text("Order #")
	buffer.text(order.OrderNumber)
	buffer.text("\n")
	buffer.command(0x1B, 0x45, 0x00)
	buffer.textSize(receiptTextSizeBody)
	buffer.text(divider(receiptWidth))
	buffer.text("\n")
	buffer.text(formatReceiptDate(order.CreatedAt))
	buffer.text("\n\n")
	for _, item := range order.Items {
		buffer.text(kitchenItemLine(item))
		buffer.text("\n")
		if description := descriptionLine(item.Description); description != "" {
			buffer.text(description)
			buffer.text("\n")
		}
		buffer.text("\n") // Add an extra newline after each item for spacing
	}
	buffer.text(divider(receiptWidth))
	buffer.text("\n\n\n\n\n\n")
	buffer.textSize(receiptTextSizeNormal)
	buffer.endText()
	buffer.command(0x1D, 0x56, 0x00)
	return buffer.bytes()
}

func salesSummaryTicket(summary OrderSummary, printedAt time.Time) []byte {
	return salesSummaryTicketWithEncoding(summary, printedAt, defaultPrinterTextEncoding)
}

func salesSummaryTicketWithEncoding(summary OrderSummary, printedAt time.Time, encoding printerTextEncoding) []byte {
	buffer := newReceiptBuffer(encoding)
	buffer.command(0x1B, 0x40)
	buffer.command(0x1B, 0x61, 0x00)
	buffer.startText()
	buffer.textSize(receiptTextSizeBody)
	buffer.command(0x1B, 0x45, 0x01)
	buffer.text("Sales Summary\n")
	buffer.command(0x1B, 0x45, 0x00)
	buffer.textSize(receiptTextSizeBody)
	buffer.text(divider(receiptWidth))
	buffer.text("\n")
	buffer.text(printedAt.In(pacificLocation).Format("01/02/06  3:04 PM"))
	buffer.text("\n\n")
	buffer.text("Orders: ")
	buffer.text(strconv.Itoa(summary.OrderCount))
	buffer.text("\n\n")
	for _, item := range summary.Items {
		buffer.text(salesSummaryItemLine(item))
		buffer.text("\n")
		if description := descriptionLine(item.Description); description != "" {
			buffer.text(description)
			buffer.text("\n")
		}
		buffer.text("\n")
	}
	if len(summary.Items) > 0 {
		buffer.text("\n")
	}
	buffer.text(totalLine(summary.TotalCents))
	buffer.text("\n\n")
	buffer.text("Printed from CashierDesk\n")
	buffer.text(divider(receiptWidth))
	buffer.text("\n\n\n\n\n\n")
	buffer.textSize(receiptTextSizeNormal)
	buffer.endText()
	buffer.command(0x1D, 0x56, 0x00)
	return buffer.bytes()
}

type receiptBuffer struct {
	buffer   bytes.Buffer
	encoding printerTextEncoding
}

func newReceiptBuffer(encoding printerTextEncoding) receiptBuffer {
	if encoding == "" {
		encoding = defaultPrinterTextEncoding
	}
	return receiptBuffer{encoding: encoding}
}

func (b *receiptBuffer) command(values ...byte) {
	b.buffer.Write(values)
}

func (b *receiptBuffer) textSize(size byte) {
	b.command(0x1D, 0x21, size)
}

func (b *receiptBuffer) startText() {
	if b.encoding == printerTextEncodingGBK {
		b.command(0x1C, 0x26)
	}
}

func (b *receiptBuffer) endText() {
	if b.encoding == printerTextEncodingGBK {
		b.command(0x1C, 0x2E)
	}
}

func (b *receiptBuffer) text(value string) {
	b.buffer.Write(encodePrinterText(value, b.encoding))
}

func (b *receiptBuffer) bytes() []byte {
	return b.buffer.Bytes()
}

func cashierItemLine(item OrderItem) string {
	prefix := strconv.Itoa(item.Quantity) + "  "
	amount := formatMoney(item.SubtotalCents)
	nameWidth := max(0, receiptWidth-receiptTextWidth(prefix)-receiptTextWidth(amount)-1)
	name := truncateReceiptText(item.Name, nameWidth)
	spaces := strings.Repeat(" ", max(1, receiptWidth-receiptTextWidth(prefix)-receiptTextWidth(name)-receiptTextWidth(amount)))
	return prefix + name + spaces + amount
}

func kitchenItemLine(item OrderItem) string {
	prefix := strconv.Itoa(item.Quantity) + "  "
	nameWidth := max(0, receiptWidth-receiptTextWidth(prefix))
	return prefix + truncateReceiptText(item.Name, nameWidth)
}

func salesSummaryItemLine(item OrderSummaryItem) string {
	prefix := "x" + strconv.Itoa(item.QuantitySold) + " "
	amount := formatMoney(item.TotalCents)
	nameWidth := max(0, receiptWidth-receiptTextWidth(prefix)-receiptTextWidth(amount)-1)
	name := truncateReceiptText(item.Name, nameWidth)
	spaces := strings.Repeat(" ", max(1, receiptWidth-receiptTextWidth(prefix)-receiptTextWidth(name)-receiptTextWidth(amount)))
	return prefix + name + spaces + amount
}

func descriptionLine(description string) string {
	description = strings.TrimSpace(stripEmoji(description))
	if description == "" {
		return ""
	}
	prefix := "   "
	return prefix + truncateReceiptText(description, receiptWidth-receiptTextWidth(prefix))
}

func totalLine(totalCents int) string {
	label := "TOTAL"
	amount := formatMoney(totalCents)
	spaces := strings.Repeat(" ", max(1, receiptWidth-len(label)-len(amount)))
	return label + spaces + amount
}

func formatMoney(cents int) string {
	return fmt.Sprintf("$%.2f", float64(cents)/100)
}

func formatReceiptDate(createdAt string) string {
	parsed, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return createdAt
	}
	return parsed.In(pacificLocation).Format("01/02/06  3:04 PM")
}

func divider(width int) string {
	return strings.Repeat("-", width)
}

func truncateReceiptText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = stripEmoji(value)
	var builder strings.Builder
	used := 0
	for _, r := range value {
		runeWidth := receiptRuneWidth(r)
		if used+runeWidth > width {
			break
		}
		builder.WriteRune(r)
		used += runeWidth
	}
	return builder.String()
}

func receiptTextWidth(value string) int {
	width := 0
	for _, r := range stripEmoji(value) {
		width += receiptRuneWidth(r)
	}
	return width
}

func receiptRuneWidth(r rune) int {
	if r <= 0x7F {
		return 1
	}
	return 2
}

func encodePrinterText(value string, encoding printerTextEncoding) []byte {
	value = stripEmoji(value)
	if encoding != printerTextEncodingGBK {
		return []byte(value)
	}

	output, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(value))
	if err != nil {
		return []byte(value)
	}
	return output
}

func stripEmoji(value string) string {
	return strings.Map(func(r rune) rune {
		if isEmojiRune(r) {
			return -1
		}
		return r
	}, value)
}

func isEmojiRune(r rune) bool {
	return r == 0x200D ||
		(r >= 0xFE00 && r <= 0xFE0F) ||
		(r >= 0x2600 && r <= 0x27BF) ||
		(r >= 0x1F000 && r <= 0x1FAFF)
}

func decodeBody(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		badRequest(w, "request body is invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		badRequest(w, "request body must contain one JSON object")
		return false
	}
	return true
}

func parseIDFromPath(w http.ResponseWriter, path string, prefix string) (int64, bool) {
	raw := strings.TrimPrefix(path, prefix)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		badRequest(w, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

func badRequest(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
}

func internalError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="OrderBackend"`)
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
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

func nowString() string {
	return time.Now().In(pacificLocation).Format(time.RFC3339)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func printerDisplayName(role string) string {
	switch role {
	case "cashier":
		return "Cashier Printer"
	case "kitchen":
		return "Kitchen Printer"
	default:
		return role + " Printer"
	}
}
