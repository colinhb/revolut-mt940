# Revolut MT940 Converter

**DISCLAIMER: This is was created for a narrow use case and may not work for you.**

Convert Revolut CSV exports to MT940 statement format.

## Usage

The program reads CSV from stdin and writes MT940 to stdout. You must provide your IBAN (or other account identifier) as a command-line argument:

```sh
cat revolut.csv | revolut-mt940 -iban NL02REVO0123456789 > mt940.sta
```

Like any other Go program, it can be installed using the Go toolchain: `go install github.com/colinhb/revolut-mt940`.

## Motivation

MT940 is a standard for communication between banks, developed and maintained by SWIFT. Whatever its other uses are in industry, one of its uses is as an export format for account transactions from banks, which may be then imported into bookkeeping software.

At this time (February 2024), the neo-bank Revolut does not offer MT940 as an export format. This program attempts to convert Revolut's CSV exports into a format close enough to MT940 that it can be imported into bookkeeping software.

## Implementation

In general, this implementation follows [SWIFT Message Reference Guide For Standards MT (November 2024): Category 9 - Cash Management and Customer Status](https://www2.swift.com/knowledgecentre/publications/us9m_20240719/2.0?topic=mt940.htm).

However, in industry, MT940 statements vary in their details, particuarly parts of Fields 61 (Statement Line) and 86 (Information to Account Owner), and this implementation does not attempt to conform to statements from any particular bank or other financial institution.

At a high level, the program parses a subset of the fields in the Revolut CSV into structs, and then uses a pair of Go templates (`document.tmpl`, `transaction.tmpl`) to format the structs into an MT940 statement. 

Unlike the Revolut CSV, an MT940 statement includes: account information (e.g. IBAN), opening and closing dates, and opening and closing balances. To deal with this, we require account information to be passed in and infer the other statement-level information from the transactions.

For other implemention issues, please review the comments below, `main.go`, and the template files.

## Comments

Extracted using `awk -f extract-readmes.awk main.go >> README.md`:

* [main.go:53] We assume the presence of a certain set of fields in the CSV, and we use those fields to build our MT940 statement, but we ignore a lot of (non-empty) fields.
* [main.go:67] We are not assuming that Revolut's CSV export will always have the same column order, so we lookup the required columns by name then find their index in the header row.
* [main.go:89] If Revolut changes any of the column headers (e.g. "Payment currency" -> "Currency"), then this will break, but it's durable to changes in column order.
* [main.go:157] Revolut CSV exports use "YYYY-MM-DD" format for dates right now, but if anything changes (like moving to using timestamps), then this may break.
* [main.go:220] We're not handling the case of reverse credit/debit transactions (i.e. "RC" and "RD" marks). For now, we're only using "D" and "C" marks.
* [main.go:225] Ideally we'd have logic to set the Swift Transaction Type appropriately based on the transaction details. Technically, this is Field 61 (Statement Line), subfield 6 (part 1, Transaction Type), and it can take on values "S", "N", or "F". For now, we're hard coding "N".
* [main.go:232] Ideally we'd have logic to set the Swift Identification Code appropriately based on the transaction details. Technically, this is Field 61 (Statement Line), subfield 6 (part 2, Identification Code), and it can take on a variety of three-letter values. For now, we're hard-coding "TRF", which is for "transfers".
* [main.go:264] Standard requires the currency to be the same for all transactions.
* [main.go:266] Correct opening dates, opening balances, closing dates, and closing balances is senstive to the order of transactions.
* [main.go:304] Revolut exports newest-first / oldest-last, but our opening- and closing-balance logic requries the opposite, so we reverese the order before formatting the MT940 document. If Revolut changes their export order, this will break. Note that we can't simply sort on date because it's not granular enough to guarantee the correct order. The right solution would be to reconstruct the order using the balance field.
* [main.go:315] MT940 format requires all transactions to be in the same currency, so we error and exit if we find more than one currency in the transactions (i.e. a set with more than one element).
