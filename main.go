// Package main converts Revolut CSV exports to Swift MT940 statement format.
package main

import (
	"embed"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"
)

//go:embed *.tmpl
var templateFS embed.FS

// Set implements a generic set data structure using a map.
// This is a thin abstrction over idiomatic use of map[]struct{} in Go as a set.
type Set[E comparable] map[E]struct{}

// The type parameter E must be comparable to allow its use as a map key.
func NewSet[E comparable]() Set[E] {
	return Set[E]{}
}

func (s Set[E]) Add(e E) {
	s[e] = struct{}{}
}

// Reduce reduces a slice to a single value using an accumulator
func Reduce[T any, A any](items []T, init A, reducer func(A, T) A) A {
	acc := init
	for _, item := range items {
		acc = reducer(acc, item)
	}
	return acc
}

// Ternary provides the functionality of a ternary operator (?:).
func Ternary[T any](condition bool, ifTrue T, ifFalse T) T {
	if condition {
		return ifTrue
	}
	return ifFalse
}

// Transaction represents a single transaction record from Revolut's CSV export.
// README: We assume the presence of a certain set of fields in the CSV, and we use
// those fields to build our MT940 statement, but we ignore a lot of (non-empty) fields.
type Transaction struct {
	DateCompleted time.Time
	ID            string
	Type          string
	Description   string
	Amount        float64
	Currency      string
	Balance       float64
	Reference     string
}

// columnIndices represents the position of required fields in the CSV.
// README: We are not assuming that Revolut's CSV export will always have the same
// column order, so we lookup the required columns by name then find their index in the
// header row.
type columnIndices struct {
	dateCompleted int
	id            int
	txType        int
	description   int
	amount        int
	currency      int
	balance       int
	reference     int
}

// findColumnIndices locates required columns in CSV headers and returns their indices.
// It takes a slice of strings (the first row of the CSV) and returns a columnIndices
// struct.
func findColumnIndices(headers []string) (columnIndices, error) {
	// Initialize indices to -1 (not found)
	indices := columnIndices{-1, -1, -1, -1, -1, -1, -1, -1}

	// Define required columns.
	// README: If Revolut changes any of the column headers (e.g. "Payment currency" ->
	// "Currency"), then this will break, but it's durable to changes in column order.
	requiredColumns := map[string]*int{
		"Date completed (UTC)": &indices.dateCompleted,
		"ID":                   &indices.id,
		"Type":                 &indices.txType,
		"Description":          &indices.description,
		"Amount":               &indices.amount,
		"Payment currency":     &indices.currency,
		"Balance":              &indices.balance,
		"Reference":            &indices.reference,
	}

	// Iterate through each header, and if one matches a required column name key
	// in the requiredColumns map, store its index position in the corresponding pointer.
	for i, header := range headers {
		if ptr, ok := requiredColumns[header]; ok {
			*ptr = i
		}
	}

	// Check for missing columns (unset indices)
	var missing []string
	for name, ptr := range requiredColumns {
		if *ptr == -1 {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return indices, fmt.Errorf("missing required columns: %v", strings.Join(missing, ", "))
	}

	// Return indices
	return indices, nil
}

// parseRevolutCSV reads a Revolut CSV export and returns a slice of parsed transactions.
func parseRevolutCSV(reader io.Reader) ([]Transaction, error) {
	csvReader := csv.NewReader(reader)

	// Read and parse header
	headers, err := csvReader.Read()
	if err != nil {
		return nil, fmt.Errorf("error reading CSV header: %v", err)
	}

	// Find required columns
	indices, err := findColumnIndices(headers)
	if err != nil {
		return nil, err
	}

	// Read and parse transactions
	var transactions []Transaction
	for {
		record, err := csvReader.Read()

		// break on EOF
		if err == io.EOF {
			break
		}

		// return error if not EOF
		if err != nil {
			return nil, fmt.Errorf("error reading CSV record: %v", err)
		}

		// Parse date
		// README: Revolut CSV exports use "YYYY-MM-DD" format for dates right now,
		// but if anything changes (like moving to using timestamps), then this may
		// break.
		completed, err := time.Parse("2006-01-02", record[indices.dateCompleted])
		if err != nil {
			return nil, fmt.Errorf("error parsing date: %v", err)
		}

		// Parse amount and balance
		amount, err := strconv.ParseFloat(record[indices.amount], 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing amount: %v", err)
		}
		balance, err := strconv.ParseFloat(record[indices.balance], 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing balance: %v", err)
		}

		// Create transaction
		tx := Transaction{
			DateCompleted: completed,
			ID:            record[indices.id],
			Type:          record[indices.txType],
			Description:   record[indices.description],
			Amount:        amount,
			Currency:      record[indices.currency],
			Balance:       balance,
			Reference:     record[indices.reference],
		}

		// Append transaction to slice
		transactions = append(transactions, tx)
	}

	// Return transactions
	return transactions, nil
}

// writeMT940 converts Transactions to a pseudo MT940 bank statement using templates.
func writeMT940(writer io.Writer, iban string, transactions []Transaction) error {
	if len(transactions) == 0 {
		return fmt.Errorf("no transactions to process")
	}

	// Create template functions
	funcMap := template.FuncMap{
		"abs": math.Abs,
		"formatDate": func(t time.Time) string {
			// Six-character date format (YYMMDD) used in MT940.
			return t.Format("060102")
		},
		"formatMoney": func(f float64) string {
			// Format money as a string with two decimal places and a comma for the
			// decimal separator.
			return strings.ReplaceAll(fmt.Sprintf("%.2f", f), ".", ",")
		},
		"escapeNewlines": func(s string) string {
			// Escape newlines by replacing them with "\n". Used for recording
			// potentially multi-line descriptions from Revolut into Field 86
			// (Information to Account Owner).
			return strings.ReplaceAll(s, "\n", "\\n")
		},
		"swiftMark": func(txn Transaction) string {
			// README: We're not handling the case of reverse credit/debit transactions
			// (i.e. "RC" and "RD" marks). For now, we're only using "D" and "C" marks.
			return Ternary(txn.Amount < 0, "D", "C")
		},
		"swiftTransactionType": func(txn Transaction) string {
			// README: Ideally we'd have logic to set the Swift Transaction Type
			// appropriately based on the transaction details. Technically, this is
			// Field 62 (Statement Line), subfield 6 (part 1, Transaction Type), and
			// it can take on values "S", "N", or "F". For now, we're hard coding "N".
			return "N"
		},
		"swiftIdentificationCode": func(txn Transaction) string {
			// README: Ideally we'd have logic to set the Swift Identification Code
			// appropriately based on the transaction details. Technically, this is
			// Field 62 (Statement Line), subfield 6 (part 2, Identification Code), and
			// it can take on a variety of three-letter values. For now, we're
			// hard-coding "TRF", which is for "transfers".
			return "TRF"
		},
	}

	// Parse templates
	tmpl, err := template.New("document.tmpl").
		Funcs(funcMap).
		ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return fmt.Errorf("error parsing templates: %v", err)
	}

	// Prepare document data
	firstTx := transactions[0]
	lastTx := transactions[len(transactions)-1]

	// Use anonymous struct to store data for template
	data := struct {
		IBAN           string
		Currency       string
		OpeningDate    time.Time
		OpeningBalance float64
		ClosingDate    time.Time
		ClosingBalance float64
		Transactions   []Transaction
	}{
		IBAN: iban,
		// README: Standard requires the currency to be the same for all transactions.
		Currency: firstTx.Currency,
		// README: Correct opening dates, opening balances, closing dates, and closing
		// balances is senstive to the order of transactions.
		OpeningDate:    firstTx.DateCompleted,
		OpeningBalance: firstTx.Balance - firstTx.Amount,
		ClosingDate:    lastTx.DateCompleted,
		ClosingBalance: lastTx.Balance,
		Transactions:   transactions,
	}

	// Execute template
	err = tmpl.ExecuteTemplate(writer, "document.tmpl", data)
	if err != nil {
		return fmt.Errorf("error executing template: %v", err)
	}

	return nil
}

// main handles CLI arguments and orchestrates the conversion process.
// Expects CSV input via stdin and outputs psuedo MT940 to stdout.
// Requires IBAN parameter, which isn't included in Revolut's CSV exports.
func main() {
	iban := flag.String("iban", "", "IBAN (required)")
	flag.Parse()

	if *iban == "" {
		fmt.Fprintf(os.Stderr, "Error: -iban flag is required\n")
		flag.Usage()
		os.Exit(1)
	}

	transactions, err := parseRevolutCSV(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing CSV: %v\n", err)
		os.Exit(1)
	}

	// Reverse transactions.
	// README: Revolut exports newest-first / oldest-last, but our opening- and
	// closing-balance logic requries the opposite, so we reverese the order before
	// formatting the MT940 document. If Revolut changes their export order, this will
	// break. Note that we can't simply sort on date because it's not granular enough to
	// guarantee the correct order. The right solution would be to reconstruct the
	// order using the balance field.
	sort.Slice(transactions, func(i, j int) bool {
		return i > j
	})

	// Check for multiple currencies in transactions.
	// README: MT940 format requires all transactions to be in the same currency, so we
	// error and exit if we find more than one currency in the transactions (i.e. a set
	// with more than one element).
	currencies := Reduce(
		transactions,
		NewSet[string](),
		func(s Set[string], txn Transaction) Set[string] {
			s.Add(txn.Currency)
			return s
		},
	)
	if len(currencies) > 1 {
		fmt.Fprintf(os.Stderr, "Error: multiple currencies detected: %v\n", currencies)
		os.Exit(1)
	}

	err = writeMT940(os.Stdout, *iban, transactions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing MT940: %v\n", err)
		os.Exit(1)
	}
}
