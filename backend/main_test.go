package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type fakePrinterService struct {
	printed []string
}

func (f *fakePrinterService) Print(printer Printer, order Order) error {
	f.printed = append(f.printed, printer.Role+"-"+order.OrderNumber)
	return nil
}

func newTestApp(t *testing.T) (*app, http.Handler) {
	t.Helper()

	db, err := openDatabase(":memory:")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	application := &app{
		store:   &store{db: db},
		printer: &fakePrinterService{},
		auth:    basicAuthConfig{Username: "test-user", Password: "test-pass"},
	}
	return application, newHandler(application)
}

func authedRequest(method string, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.SetBasicAuth("test-user", "test-pass")
	return request
}

func TestHealthEndpoint(t *testing.T) {
	_, handler := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status body field = %q, want ok", body["status"])
	}
}

func TestAPIRequiresBasicAuth(t *testing.T) {
	_, handler := newTestApp(t)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/catalog", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if authenticate := response.Header().Get("WWW-Authenticate"); authenticate != `Basic realm="OrderBackend"` {
		t.Fatalf("WWW-Authenticate = %q, want Basic realm", authenticate)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "authentication required" {
		t.Fatalf("error = %q, want authentication required", body["error"])
	}
}

func TestAPIRejectsInvalidBasicAuth(t *testing.T) {
	_, handler := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog", nil)
	request.SetBasicAuth("test-user", "wrong-pass")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestInvalidBasicAuthIsLoggedWithoutPassword(t *testing.T) {
	var logs bytes.Buffer
	originalOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(originalOutput) })

	_, handler := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog", nil)
	request.SetBasicAuth("test-user", "wrong-pass")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	logOutput := logs.String()
	if !strings.Contains(logOutput, "authentication failed") {
		t.Fatalf("log output = %q, want failed authentication message", logOutput)
	}
	if !strings.Contains(logOutput, `method=GET path=/api/v1/catalog`) {
		t.Fatalf("log output = %q, want method and path", logOutput)
	}
	if !strings.Contains(logOutput, `username="test-user"`) {
		t.Fatalf("log output = %q, want attempted username", logOutput)
	}
	if strings.Contains(logOutput, "wrong-pass") {
		t.Fatalf("log output leaked password: %q", logOutput)
	}
}

func TestCatalogReturnsOnlyActiveSaleItems(t *testing.T) {
	application, handler := newTestApp(t)
	inactive, err := application.store.createSaleItem(SaleItem{Name: "Hidden Item", PriceCents: 999, Active: false})
	if err != nil {
		t.Fatalf("create inactive sale item: %v", err)
	}
	if err := application.store.setSaleItemActive(inactive.ID, false); err != nil {
		t.Fatalf("deactivate sale item: %v", err)
	}

	request := authedRequest(http.MethodGet, "/api/v1/catalog", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var items []SaleItem
	if err := json.NewDecoder(response.Body).Decode(&items); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("catalog length = %d, want default 5 active items", len(items))
	}
	for _, item := range items {
		if item.Name == "Hidden Item" {
			t.Fatal("catalog included inactive item")
		}
	}
}

func TestSaleItemCRUD(t *testing.T) {
	_, handler := newTestApp(t)
	createBody := []byte(`{"name":"Egg Tart","price_cents":350,"active":true}`)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, authedRequest(http.MethodPost, "/api/v1/sale-items", bytes.NewReader(createBody)))

	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", createResponse.Code, http.StatusCreated)
	}
	var created SaleItem
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created item: %v", err)
	}
	if created.ID == 0 || created.Name != "Egg Tart" || created.PriceCents != 350 || !created.Active {
		t.Fatalf("created item = %+v, want Egg Tart", created)
	}

	updateBody := []byte(`{"name":"Egg Tart","price_cents":400,"active":false}`)
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, authedRequest(http.MethodPut, "/api/v1/sale-items/"+jsonNumber(created.ID), bytes.NewReader(updateBody)))

	var updated SaleItem
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated item: %v", err)
	}
	if updated.PriceCents != 400 || updated.Active {
		t.Fatalf("updated item = %+v, want inactive item at 400 cents", updated)
	}
}

func TestPrinterConfigCanBeSavedAndLoaded(t *testing.T) {
	_, handler := newTestApp(t)
	body := []byte(`{"host":"192.168.1.50","port":9100,"enabled":true}`)
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, authedRequest(http.MethodPut, "/api/v1/printers/cashier", bytes.NewReader(body)))

	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d", updateResponse.Code, http.StatusOK)
	}
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, authedRequest(http.MethodGet, "/api/v1/printers/cashier", nil))

	var printer Printer
	if err := json.NewDecoder(getResponse.Body).Decode(&printer); err != nil {
		t.Fatalf("decode printer: %v", err)
	}
	if printer.Host != "192.168.1.50" || printer.Port != 9100 || !printer.Enabled {
		t.Fatalf("printer = %+v, want saved cashier printer", printer)
	}
}

func TestCreateOrderUsesCatalogSnapshotAndCreatesPrintJobs(t *testing.T) {
	application, handler := newTestApp(t)
	items, err := application.store.listSaleItems(true)
	if err != nil {
		t.Fatalf("list sale items: %v", err)
	}
	body := []byte(`{"items":[{"sale_item_id":` + jsonNumber(items[0].ID) + `,"quantity":2}]}`)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(body)))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	var order Order
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		t.Fatalf("decode created order: %v", err)
	}
	if order.ID != 1 || order.OrderNumber != "1001" {
		t.Fatalf("order id/number = %d/%q, want 1/1001", order.ID, order.OrderNumber)
	}
	if len(order.Items) != 1 || order.Items[0].Name != items[0].Name || order.Items[0].SubtotalCents != items[0].PriceCents*2 {
		t.Fatalf("order items = %+v, want catalog snapshot", order.Items)
	}
	if order.TotalCents != items[0].PriceCents*2 {
		t.Fatalf("total_cents = %d, want %d", order.TotalCents, items[0].PriceCents*2)
	}
	if len(order.PrintJobs) != 2 {
		t.Fatalf("print job count = %d, want 2", len(order.PrintJobs))
	}
}

func TestCreateOrderRejectsInactiveItem(t *testing.T) {
	application, handler := newTestApp(t)
	item, err := application.store.createSaleItem(SaleItem{Name: "Sold Out", PriceCents: 100, Active: false})
	if err != nil {
		t.Fatalf("create sale item: %v", err)
	}
	body := []byte(`{"items":[{"sale_item_id":` + jsonNumber(item.ID) + `,"quantity":1}]}`)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(body)))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestLegacyOrdersEndpointStillCreatesSnapshotOrder(t *testing.T) {
	_, handler := newTestApp(t)
	body := []byte(`{"items":[{"name":"Milk Tea","price":4.5,"quantity":2}],"total":9}`)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, authedRequest(http.MethodPost, "/orders", bytes.NewReader(body)))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	var order Order
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	if order.OrderNumber != "1001" || order.TotalCents != 900 || order.Items[0].Name != "Milk Tea" {
		t.Fatalf("legacy order = %+v, want Milk Tea snapshot", order)
	}
}

func TestOpenDatabaseRenamesPrototypeOrdersTable(t *testing.T) {
	path := t.TempDir() + "/orders.db"
	oldDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open old database: %v", err)
	}
	if _, err := oldDB.Exec(`CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, items TEXT NOT NULL, total REAL NOT NULL)`); err != nil {
		t.Fatalf("create old orders table: %v", err)
	}
	if _, err := oldDB.Exec(`INSERT INTO orders (created_at, items, total) VALUES ('2026-01-01T00:00:00-08:00', '[]', 0)`); err != nil {
		t.Fatalf("insert old order: %v", err)
	}
	oldDB.Close()

	db, err := openDatabase(path)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer db.Close()

	var orderNumberColumnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('orders') WHERE name = 'order_number'`).Scan(&orderNumberColumnCount); err != nil {
		t.Fatalf("inspect new orders table: %v", err)
	}
	if orderNumberColumnCount != 1 {
		t.Fatalf("order_number column count = %d, want 1", orderNumberColumnCount)
	}

	var legacyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM legacy_orders_v0`).Scan(&legacyCount); err != nil {
		t.Fatalf("query legacy table: %v", err)
	}
	if legacyCount != 1 {
		t.Fatalf("legacy order count = %d, want 1", legacyCount)
	}
}

func TestCashierTicketMatchesCommittedReceiptInstructions(t *testing.T) {
	order := testPrintOrder()
	ticket := cashierTicket(order)
	text := StringFromASCII(ticket)

	if got := []byte(ticket[:2]); !bytes.Equal(got, []byte{0x1B, 0x40}) {
		t.Fatalf("ticket prefix = %v, want ESC @", got)
	}
	if got := []byte(ticket[2:5]); !bytes.Equal(got, []byte{0x1B, 0x61, 0x00}) {
		t.Fatalf("alignment command = %v, want left align", got)
	}
	for _, want := range []string{
		"Order #1001\n",
		"------------------------------",
		"07/29/26  9:37 AM",
		"2  Milk Tea             $11.00",
		"1  Green Tea             $5.00",
		"TOTAL                   $16.00",
		"Thank you!",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cashier ticket missing %q in %q", want, text)
		}
	}
	if got := []byte(ticket[len(ticket)-9 : len(ticket)-3]); !bytes.Equal(got, []byte{'\n', '\n', '\n', '\n', '\n', '\n'}) {
		t.Fatalf("feed bytes = %v, want six newlines", got)
	}
	if got := []byte(ticket[len(ticket)-3:]); !bytes.Equal(got, []byte{0x1D, 0x56, 0x00}) {
		t.Fatalf("cut bytes = %v, want GS V 0", got)
	}
}

func TestKitchenTicketIsFoodOnlyWithOrderNumber(t *testing.T) {
	order := testPrintOrder()
	ticket := kitchenTicket(order)
	text := StringFromASCII(ticket)

	for _, want := range []string{
		"Order #1001\n",
		"07/29/26  9:37 AM",
		"2  Milk Tea",
		"1  Green Tea",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("kitchen ticket missing %q in %q", want, text)
		}
	}
	for _, notWant := range []string{"$11.00", "$5.00", "TOTAL", "Thank you!"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("kitchen ticket should not contain %q in %q", notWant, text)
		}
	}
	if got := []byte(ticket[5:8]); !bytes.Equal(got, []byte{0x1D, 0x21, 0x11}) {
		t.Fatalf("kitchen order number size command = %v, want double size", got)
	}
	if got := []byte(ticket[len(ticket)-3:]); !bytes.Equal(got, []byte{0x1D, 0x56, 0x00}) {
		t.Fatalf("cut bytes = %v, want GS V 0", got)
	}
}

func TestTCPPrinterServiceWritesRoleSpecificTicket(t *testing.T) {
	connection := &fakeNetConn{}
	printer := TCPPrinterService{
		Timeout: time.Second,
		Dial: func(network string, address string, timeout time.Duration) (net.Conn, error) {
			if network != "tcp" {
				t.Fatalf("network = %q, want tcp", network)
			}
			if address != "192.168.1.50:9100" {
				t.Fatalf("address = %q, want printer host and port", address)
			}
			if timeout != time.Second {
				t.Fatalf("timeout = %s, want 1s", timeout)
			}
			return connection, nil
		},
	}

	err := printer.Print(Printer{Role: "kitchen", Host: "192.168.1.50", Port: 9100, Enabled: true}, testPrintOrder())

	if err != nil {
		t.Fatalf("print error = %v, want nil", err)
	}
	if !connection.deadlineSet {
		t.Fatal("expected print deadline to be set")
	}
	if !connection.closed {
		t.Fatal("expected connection to be closed")
	}
	text := StringFromASCII(connection.written.Bytes())
	if !strings.Contains(text, "Order #1001") || strings.Contains(text, "TOTAL") {
		t.Fatalf("written ticket = %q, want kitchen ticket", text)
	}
}

func TestTCPPrinterServiceReportsDialAndWriteErrors(t *testing.T) {
	dialErr := errors.New("dial failed")
	dialFailure := TCPPrinterService{
		Dial: func(string, string, time.Duration) (net.Conn, error) {
			return nil, dialErr
		},
	}
	if err := dialFailure.Print(Printer{Role: "cashier", Host: "192.168.1.50", Port: 9100}, testPrintOrder()); !errors.Is(err, dialErr) {
		t.Fatalf("dial error = %v, want %v", err, dialErr)
	}

	writeErr := errors.New("write failed")
	writeFailure := TCPPrinterService{
		Dial: func(string, string, time.Duration) (net.Conn, error) {
			return &fakeNetConn{writeErr: writeErr}, nil
		},
	}
	if err := writeFailure.Print(Printer{Role: "cashier", Host: "192.168.1.50", Port: 9100}, testPrintOrder()); !errors.Is(err, writeErr) {
		t.Fatalf("write error = %v, want %v", err, writeErr)
	}
}

func TestDispatchPrintJobsLogsDisabledPrinters(t *testing.T) {
	application, _ := newTestApp(t)
	items, err := application.store.listSaleItems(true)
	if err != nil {
		t.Fatalf("list sale items: %v", err)
	}
	order, jobs, err := application.store.createOrder(createOrderRequest{
		Items: []createOrderItemRequest{{SaleItemID: items[0].ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	var logOutput bytes.Buffer
	originalOutput := log.Writer()
	log.SetOutput(&logOutput)
	t.Cleanup(func() { log.SetOutput(originalOutput) })

	application.dispatchPrintJobs(order.ID, jobs)

	logText := logOutput.String()
	if !strings.Contains(logText, "disabled printers for order #1001: cashier, kitchen") {
		t.Fatalf("log output = %q, want disabled printer roles", logText)
	}
	stored, err := application.store.getOrder(order.ID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	for _, job := range stored.PrintJobs {
		if job.Status != "skipped" || job.LastError != "printer is disabled" {
			t.Fatalf("print job = %+v, want skipped disabled job", job)
		}
	}
}

func TestUnknownRouteReturnsJSONNotFound(t *testing.T) {
	_, handler := newTestApp(t)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, authedRequest(http.MethodGet, "/unknown", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}

func testPrintOrder() Order {
	return Order{
		ID:          1,
		OrderNumber: "1001",
		CreatedAt:   "2026-07-29T09:37:00-07:00",
		Items: []OrderItem{
			{Name: "Milk Tea", UnitPriceCents: 550, Quantity: 2, SubtotalCents: 1100},
			{Name: "Green Tea", UnitPriceCents: 500, Quantity: 1, SubtotalCents: 500},
		},
		TotalCents: 1600,
	}
}

func StringFromASCII(data []byte) string {
	return string(data)
}

type fakeNetConn struct {
	written     bytes.Buffer
	writeErr    error
	deadlineSet bool
	closed      bool
}

func (c *fakeNetConn) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (c *fakeNetConn) Write(data []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return c.written.Write(data)
}

func (c *fakeNetConn) Close() error {
	c.closed = true
	return nil
}

func (c *fakeNetConn) LocalAddr() net.Addr {
	return fakeAddr("local")
}

func (c *fakeNetConn) RemoteAddr() net.Addr {
	return fakeAddr("remote")
}

func (c *fakeNetConn) SetDeadline(time.Time) error {
	c.deadlineSet = true
	return nil
}

func (c *fakeNetConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *fakeNetConn) SetWriteDeadline(time.Time) error {
	return nil
}

type fakeAddr string

func (a fakeAddr) Network() string {
	return string(a)
}

func (a fakeAddr) String() string {
	return string(a)
}
