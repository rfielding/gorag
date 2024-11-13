package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	_ "github.com/lib/pq"
)

type DBMetadata struct {
	Tables map[string][]string // Map of table names to column lists
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
}

type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func connectToDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	return db, nil
}

/*
  We get the schema explicitly so that chatgpt can study it to
  plan SQL queries. This lets it not only understand questions
  in terms of tables and columns, but in terms of joins and types.
 */
func getSchema(db *sql.DB) (*DBMetadata, error) {
	query := `
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position;
	`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	metadata := DBMetadata{Tables: make(map[string][]string)}
	var tableName, columnName string
	for rows.Next() {
		err := rows.Scan(&tableName, &columnName)
		if err != nil {
			return nil, err
		}
		metadata.Tables[tableName] = append(metadata.Tables[tableName], columnName)
	}
	return &metadata, nil
}

func formatSchema(metadata *DBMetadata) string {
	var sb strings.Builder
	for table, columns := range metadata.Tables {
		sb.WriteString(fmt.Sprintf("Table: %s\nColumns: %s\n", table, strings.Join(columns, ", ")))
	}
	return sb.String()
}

/*
  If you want to pass in extra metadata to explain things that must be described outside the schema,
  then put that here. It's basically just an extra bit of system prompting.
 */
func loadExtraMetadata(filename string) (map[string]string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var extraMetadata map[string]string
	if err := json.Unmarshal(data, &extraMetadata); err != nil {
		return nil, err
	}
	return extraMetadata, nil
}

func callOpenAIRaw(apiKey, prompt string) ([]byte, error) {
	url := "https://api.openai.com/v1/chat/completions"
	requestBody, err := json.Marshal(OpenAIRequest{
		Model: "gpt-4o",
		// Just using user prompting for now
		Messages: []Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
		Temperature: 0.7,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, err
}

func callOpenAI(apiKey, prompt string) (string, error) {
	body, err := callOpenAIRaw(apiKey, prompt)
	if err != nil {
		return "", err
	}
	// we need to be careful, because asking it to only render json
	// does not work. it currently wants to put a markdown json
	// fence around the json result, so we parse it to just
	// assume that the first { starts and last } ends json.
	// it's kind of nuts that this is not the easiest thing to
	// make it obey.
	var openAIResponse OpenAIResponse
	if err := json.Unmarshal(body, &openAIResponse); err != nil {
		return "", err
	}
	if len(openAIResponse.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}

	// Extract and parse JSON from the response content
	responseContentRaw := openAIResponse.Choices[0].Message.Content
	var queryResponse struct {
		// We use the query field to mean the SQL query
		Query string `json:"query"`
	}
	responseContent := findJson(responseContentRaw)
	if err := json.Unmarshal([]byte(responseContent), &queryResponse); err != nil {
		return "", fmt.Errorf(
			"failed to parse JSON response: %v\n%s",
			err,
			responseContent,
		)
	}

	return findJson(queryResponse.Query), nil
}

// Just assume that the json markdown fence is the only place with curlies
func findJson(content string) string {
	if strings.Index(content, "{") > 0 {
		if strings.LastIndex(content, "}") > 0 {
			content = content[strings.Index(content, "{") : strings.LastIndex(content, "}")+1]
		}
	}
	return content
}

// connect to a postgres database
var user = flag.String("user", "llama", "user name")
var password = flag.String("password", "llama", "password")
var dbname = flag.String("dbname", "memory_agent", "database name")
var host = flag.String("host", "localhost", "host name")
var prompt = flag.String("prompt", "How many rows are in the conversation?", "user's request")

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	// Connect to database
	flag.Parse()
	dsn := fmt.Sprintf(
		"user=%s password=%s dbname=%s host=%s",
		*user, *password, *dbname, *host,
	)
	db, err := connectToDB(dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()
	log.Println("Connected to database")

	// Retrieve schema
	schema, err := getSchema(db)
	if err != nil {
		log.Fatalf("Failed to retrieve schema: %v", err)
	}
	log.Println("Retrieved schema")

	// Format schema for OpenAI prompt
	schemaStr := formatSchema(schema)

	// Load additional metadata (if any)
	extraMetadataFile := "metadata.json"
	extraMetadata, err := loadExtraMetadata(extraMetadataFile)
	if err != nil {
		fmt.Println("No extra metadata found, continuing without it.")
		extraMetadata = make(map[string]string)
	}
	log.Printf("Loaded metadata")

	// Prepare user input and system prompt
	userInput := *prompt
	systemPrompt := fmt.Sprintf(`
You are an AI that generates PostgreSQL SQL queries based on a user's natural language request.
The database schema is as follows:

%s

Additionally, here is some extra information that might help interpret specific tables or columns:

%v

If the prompt is a valid postgres query, then take it literally and
just return json with the query field set to the prompt.
The SQL queries can be complex, joined, with subqueries, etc;
because the schema can be consulted to figure it out.
http response must be application/json, with the sql query in it:
{ "query": "<SQL query here>" }

User's request: %s
`, schemaStr, extraMetadata, userInput)

	// Call OpenAI to generate the SQL query in JSON format
	query, err := callOpenAI(apiKey, systemPrompt)
	if err != nil {
		log.Fatalf("Failed to generate SQL: %v", err)
	}

	// Execute query
	log.Printf("Got SQL query: %s\n", query)
	rows, err := db.Query(query)
	if err != nil {
		log.Fatalf("Failed to execute query: %v", err)
	}
	defer rows.Close()

	// Dynamically process query results based on returned columns
	columns, err := rows.Columns()
	if err != nil {
		log.Fatalf("Failed to get columns: %v", err)
	}
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	result := make([]string, 0)
	for rows.Next() {
		err := rows.Scan(valuePtrs...)
		if err != nil {
			log.Fatalf("Failed to scan row: %v", err)
		}

		// Print row values
		for i, col := range columns {
			var v interface{}
			switch values[i].(type) {
			case []byte:
				v = string(values[i].([]byte))
			default:
				v = values[i]
			}
			result = append(
				result,
				fmt.Sprintf("%s: %v", col, v),
			)
		}
	}
	resultStr := strings.Join(result, "\n")

	systemPrompt2 := fmt.Sprintf(`
	We are doing RAG atainst a database with this schema

	%s

	with some extra metadata possibly

	%s

	The user prompt was

	%s

	And the resulting query was

	%s
	`, schemaStr, extraMetadata, userInput, resultStr)
	body, err := callOpenAIRaw(apiKey, systemPrompt2)
	if err != nil {
		log.Fatalf("Failed to generate SQL: %v", err)
	}

	var openAIResponse OpenAIResponse
	if err := json.Unmarshal(body, &openAIResponse); err != nil {
		log.Fatal(err)
	}
	log.Print("\n%\n", resultStr)
	log.Printf("%s", openAIResponse.Choices[0].Message.Content)
}
