package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	defaultConfigPath = "/etc/yaagwadb/config.toml"
	defaultListen     = "127.0.0.1:8080"
	exitFailure       = 1
	jsonIndentPrefix  = ""
	jsonIndent        = "  "
	requestTimeout    = 30 * time.Second
	addressFormat     = "http://%s"
	versionPath       = "/v3"
	pipelinePath      = "/v3/pipeline"
	contentType       = "application/json"
	// fileNameFormat names the file the test writes, after the current time, so that runs do not collide.
	fileNameFormat = "hrana-%s.txt"
	fileTimeFormat = "20060102-150405.000000000"
)

const (
	firstContent  = "first content"
	secondContent = "second content"
	tagValue      = "hrana-test"
	xattrFormat   = `{"user.tag":%q}`
)

const (
	statementInsert      = `INSERT INTO objects (path, body) VALUES (?, ?)`
	statementUpsert      = `INSERT INTO objects (path, body) VALUES (?, ?) ON CONFLICT (path) DO UPDATE SET body = excluded.body`
	statementSelectBody  = `SELECT body, size FROM objects WHERE path = ?`
	statementUpdateXattr = `UPDATE objects SET xattr = ? WHERE path = ?`
	statementSelectByTag = `SELECT path FROM objects WHERE xattr ->> 'user.tag' = ?`
	statementCount       = `SELECT COUNT(*) FROM objects WHERE path = ?`
	statementDelete      = `DELETE FROM objects WHERE path = ?`
	statementInternal    = `SELECT * FROM inode`
	statementBegin       = `BEGIN`
	statementBodyInWhere = `SELECT path FROM objects WHERE body = ?`
	statementLimitOver   = `SELECT path FROM objects LIMIT 1000000000`
	requestTypeExecute   = "execute"
	requestTypeClose     = "close"
	resultTypeOK         = "ok"
	valueTypeText        = "text"
	valueTypeInteger     = "integer"
	codeExists           = "EEXIST"
	codeInvalidArgument  = "EINVAL"
	codeNotSupported     = "ENOTSUP"
	expectedOneRow       = 1
	expectedNoRows       = 0
	expectedOneAffected  = 1
	firstRow             = 0
	firstColumn          = 0
	secondColumn         = 1
	decimalBase          = 10
	integerBitSize       = 64
)

const (
	usageConfig     = "Configuration file (the [hrana] listen address is read from it)"
	usageSQL        = "Run this one statement, print the response and exit, instead of the checks"
	noStatement     = ""
	stepPassed      = "ok    %s\n"
	stepFailed      = "FAIL  %s: %v\n"
	summaryPassed   = "\nall %d checks passed (%s)\n"
	summaryFailed   = "\n%d of %d checks failed\n"
	statusFormat    = "status %d"
	resultFormat    = "got %+v"
	errorFormat     = "error %s (%s)"
	wantErrorFormat = "want error %s, got %+v"
	valueFormat     = "got %q, want %q"
)

const (
	stepGetV3                              = "GET /v3"
	stepInsert                             = "INSERT"
	stepInsertOfAnExistingPathIsEexist     = "INSERT of an existing path is EEXIST"
	stepSelectBody                         = "SELECT body"
	stepUpdateXattr                        = "UPDATE xattr"
	stepSelectByXattr                      = "SELECT by xattr"
	stepInsertOnConflictDoUpdateOverwrites = "INSERT ON CONFLICT DO UPDATE overwrites"
	stepInternalTablesAreRefused           = "internal tables are refused"
	stepTransactionsAreRefused             = "transactions are refused"
	stepBodyInWhereIsRefused               = "body in WHERE is refused"
	stepLimitOverTheRowLimitIsRefused      = "LIMIT over the row limit is refused"
	stepDelete                             = "DELETE"
)

type settings struct {
	Hrana struct {
		Listen string `toml:"listen"`
	} `toml:"hrana"`
}

type value struct {
	Type  string `json:"type"`
	Value any    `json:"value,omitempty"`
}

type statement struct {
	SQL  string  `json:"sql"`
	Args []value `json:"args"`
}

type streamRequest struct {
	Type string     `json:"type"`
	Stmt *statement `json:"stmt,omitempty"`
}

type pipelineRequest struct {
	Requests []streamRequest `json:"requests"`
}

type result struct {
	Type     string `json:"type"`
	Response *struct {
		Result *struct {
			Rows             [][]value `json:"rows"`
			AffectedRowCount int64     `json:"affected_row_count"`
		} `json:"result"`
	} `json:"response"`
	Error *struct {
		Message string  `json:"message"`
		Code    *string `json:"code"`
	} `json:"error"`
}

type tester struct {
	address string
	client  *http.Client
	passed  int
	failed  int
}

func text(input string) value { return value{Type: valueTypeText, Value: input} }

func encoded(input string) value { return text(base64.StdEncoding.EncodeToString([]byte(input))) }

func main() {
	configPath := flag.String("c", defaultConfigPath, usageConfig)
	statementText := flag.String("sql", noStatement, usageSQL)
	flag.Parse()
	var configuration settings
	configuration.Hrana.Listen = defaultListen
	if _, err := toml.DecodeFile(*configPath, &configuration); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitFailure)
	}
	test := &tester{address: fmt.Sprintf(addressFormat, configuration.Hrana.Listen), client: &http.Client{Timeout: requestTimeout}}
	if *statementText != noStatement {
		if err := test.print(*statementText); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitFailure)
		}
		return
	}
	path := fmt.Sprintf(fileNameFormat, time.Now().Format(fileTimeFormat))
	test.run(path)
	total := test.passed + test.failed
	if test.failed > 0 {
		fmt.Printf(summaryFailed, test.failed, total)
		os.Exit(exitFailure)
	}
	fmt.Printf(summaryPassed, total, path)
}

func (t *tester) run(path string) {
	t.check(stepGetV3, func() error {
		response, err := t.client.Get(t.address + versionPath)
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf(statusFormat, response.StatusCode)
		}
		return nil
	})
	t.check(stepInsert, func() error {
		return t.wantAffected(statementInsert, expectedOneAffected, text(path), encoded(firstContent))
	})
	t.check(stepInsertOfAnExistingPathIsEexist, func() error {
		return t.wantError(statementInsert, codeExists, text(path), encoded(secondContent))
	})
	t.check(stepSelectBody, func() error { return t.wantBody(path, firstContent) })
	t.check(stepUpdateXattr, func() error {
		return t.wantAffected(statementUpdateXattr, expectedOneAffected, text(fmt.Sprintf(xattrFormat, tagValue)), text(path))
	})
	t.check(stepSelectByXattr, func() error {
		rows, err := t.rows(statementSelectByTag, text(tagValue))
		if err != nil {
			return err
		}
		if len(rows) != expectedOneRow || rows[firstRow][firstColumn].Value != path {
			return fmt.Errorf(resultFormat, rows)
		}
		return nil
	})
	t.check(stepInsertOnConflictDoUpdateOverwrites, func() error {
		if err := t.wantAffected(statementUpsert, expectedOneAffected, text(path), encoded(secondContent)); err != nil {
			return err
		}
		return t.wantBody(path, secondContent)
	})
	t.check(stepInternalTablesAreRefused, func() error { return t.wantError(statementInternal, codeInvalidArgument) })
	t.check(stepTransactionsAreRefused, func() error { return t.wantError(statementBegin, codeNotSupported) })
	t.check(stepBodyInWhereIsRefused, func() error {
		return t.wantError(statementBodyInWhere, codeInvalidArgument, text(firstContent))
	})
	t.check(stepLimitOverTheRowLimitIsRefused, func() error { return t.wantError(statementLimitOver, codeInvalidArgument) })
	t.check(stepDelete, func() error {
		if err := t.wantAffected(statementDelete, expectedOneAffected, text(path)); err != nil {
			return err
		}
		rows, err := t.rows(statementCount, text(path))
		if err != nil {
			return err
		}
		if count := integer(rows[firstRow][firstColumn]); count != expectedNoRows {
			return fmt.Errorf(resultFormat, rows)
		}
		return nil
	})
}

func (t *tester) check(name string, step func() error) {
	if err := step(); err != nil {
		t.failed++
		fmt.Printf(stepFailed, name, err)
		return
	}
	t.passed++
	fmt.Printf(stepPassed, name)
}

func (t *tester) execute(sql string, arguments ...value) (*result, error) {
	request := pipelineRequest{Requests: []streamRequest{
		{Type: requestTypeExecute, Stmt: &statement{SQL: sql, Args: arguments}},
		{Type: requestTypeClose},
	}}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	response, err := t.client.Post(t.address+pipelinePath, contentType, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(statusFormat, response.StatusCode)
	}
	var decoded struct {
		Results []result `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Results) == 0 {
		return nil, fmt.Errorf(resultFormat, decoded)
	}
	return &decoded.Results[firstRow], nil
}

func (t *tester) succeeded(sql string, arguments ...value) (*result, error) {
	executed, err := t.execute(sql, arguments...)
	if err != nil {
		return nil, err
	}
	if executed.Type != resultTypeOK {
		code := ""
		if executed.Error.Code != nil {
			code = *executed.Error.Code
		}
		return nil, fmt.Errorf(errorFormat, code, executed.Error.Message)
	}
	return executed, nil
}

func (t *tester) rows(sql string, arguments ...value) ([][]value, error) {
	executed, err := t.succeeded(sql, arguments...)
	if err != nil {
		return nil, err
	}
	return executed.Response.Result.Rows, nil
}

func (t *tester) wantAffected(sql string, affected int64, arguments ...value) error {
	executed, err := t.succeeded(sql, arguments...)
	if err != nil {
		return err
	}
	if executed.Response.Result.AffectedRowCount != affected {
		return fmt.Errorf(resultFormat, executed.Response.Result)
	}
	return nil
}

func (t *tester) wantError(sql, code string, arguments ...value) error {
	executed, err := t.execute(sql, arguments...)
	if err != nil {
		return err
	}
	if executed.Type == resultTypeOK || executed.Error.Code == nil || *executed.Error.Code != code {
		return fmt.Errorf(wantErrorFormat, code, executed)
	}
	return nil
}

func (t *tester) wantBody(path, content string) error {
	rows, err := t.rows(statementSelectBody, text(path))
	if err != nil {
		return err
	}
	if len(rows) != expectedOneRow {
		return fmt.Errorf(resultFormat, rows)
	}
	want := base64.StdEncoding.EncodeToString([]byte(content))
	if got, _ := rows[firstRow][firstColumn].Value.(string); got != want {
		return fmt.Errorf(valueFormat, got, want)
	}
	if size := integer(rows[firstRow][secondColumn]); size != int64(len(content)) {
		return fmt.Errorf(resultFormat, rows)
	}
	return nil
}

func integer(input value) int64 {
	text, _ := input.Value.(string)
	parsed, _ := strconv.ParseInt(text, decimalBase, integerBitSize)
	return parsed
}

func (t *tester) print(sql string) error {
	executed, err := t.execute(sql)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent(jsonIndentPrefix, jsonIndent)
	return encoder.Encode(executed)
}
