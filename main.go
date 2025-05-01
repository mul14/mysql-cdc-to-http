package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/goccy/go-yaml"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

var (
	positionFile = "./storage/binlog_position.json"
	postURL      = "http://localhost:8080"
	tableGroups  map[string][]string
	tableToGroup map[string]string
	redisClient  *redis.Client
	redisCtx     = context.Background()
	log          = logrus.New()
	logLevel     = "info"
)

const (
	redisPositionKey = "binlog_position"
	redisQueueKey    = "cdc_events"
)

type BinlogPosition struct {
	Name string `json:"name"`
	Pos  uint32 `json:"pos"`
}

type eventHandler struct {
	canal.DummyEventHandler
}

func (h *eventHandler) OnRow(e *canal.RowsEvent) error {
	group := getGroupForTable(e.Table.Name)
	if group == "" {
		log.Tracef("Skipping ungrouped table: %s\n", e.Table.Name)
		return nil
	}

	// Only process Insert or Update events
	if e.Action != canal.InsertAction && e.Action != canal.UpdateAction {
		return nil
	}

	columnNames := getColumnNames(e)

	for i := 0; i < len(e.Rows); i++ {
		var before map[string]interface{}
		var after map[string]interface{}

		if e.Action == canal.UpdateAction {
			before = rowToMap(e, columnNames, e.Rows[i])
			i++
			if i >= len(e.Rows) {
				break
			}
			after = rowToMap(e, columnNames, e.Rows[i])
		} else {
			before = nil
			after = rowToMap(e, columnNames, e.Rows[i])
		}

		payload := map[string]interface{}{
			"before": before,
			"after":  after,
			"source": map[string]interface{}{
				"table": e.Table.Name,
			},
		}

		jsonData, err := json.Marshal(payload)
		if err != nil {
			log.Errorln("Error marshaling JSON:", err)
			continue
		}

		if redisClient != nil {
			err = redisClient.RPush(redisCtx, redisQueueKey, jsonData).Err()
			if err != nil {
				log.Warningln("Error pushing data to Redis queue:", err)
			} else {
				log.Debugf("Queued event to Redis list '%s'\n", redisQueueKey)
			}
		} else {
			log.Warningln("Redis unavailable, skipping event queueing.")
		}

		tableURL := postURL + "/" + group
		log.Infof("Sending HTTP POST request to %s with payload: %s\n", tableURL, jsonData)
		resp, err := http.Post(tableURL, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Errorln("Error sending HTTP POST:", err)
			continue
		}
		_ = resp.Body.Close()
		log.Debugf("Successfully sent queued event to %s\n", tableURL)
	}

	return nil
}

func (h *eventHandler) OnPosSynced(header *replication.EventHeader, pos mysql.Position, gtid mysql.GTIDSet, isMaster bool) error {
	log.Tracef("Position synced: %s\n", pos)
	savePosition(pos)
	return nil
}

func rowToMap(e *canal.RowsEvent, columns []string, row []interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for i, col := range columns {
		val := row[i]
		if val == nil {
			result[col] = nil
			continue
		}

		if i < len(e.Table.Columns) {
			column := e.Table.Columns[i]
			if isTextType(column) {
				if strVal, ok := val.([]byte); ok {
					decoded, err := base64.StdEncoding.DecodeString(string(strVal))
					if err != nil {
						result[col] = string(strVal)
					} else {
						result[col] = string(decoded)
					}
				} else {
					result[col] = val
				}
			} else if isDateType(column) {
				strVal := fmt.Sprintf("%s", val)
				t, err := time.Parse("2006-01-02", strVal)
				if err == nil {
					result[col] = t.Format(time.RFC3339)
				} else {
					result[col] = strVal
				}
			} else if isDateTimeType(column) {
				result[col] = val
			} else if isBooleanType(column) {
				if val == 1 || val == true {
					result[col] = true
				} else if val == 0 || val == false {
					result[col] = false
				} else {
					result[col] = false
				}
			} else if isBitType(column) {
				switch v := val.(type) {
				case []byte:
					// MySQL BIT(1) is often returned as []byte{0x00} or []byte{0x01}
					if len(v) > 0 && v[0] != 0 {
						result[col] = true
					} else {
						result[col] = false
					}
				case bool:
					result[col] = v
				default:
					if val == 1 {
						result[col] = true
					} else {
						result[col] = false
					}
				}
			} else if isFloatType(column) {
				if num, ok := val.(json.Number); ok {
					result[col], _ = num.Float64()
				} else {
					result[col] = val
				}
			} else if isIntType(column) {
				if num, ok := val.(json.Number); ok {
					result[col], _ = num.Int64()
				} else {
					result[col] = val
				}
			} else {
				result[col] = val
			}

			log.Printf(
				"Processed column: %s.%s, type: %d, rawType: %s, origValue: %v, newValue: %v\n",
				e.Table.Name, column.Name, column.Type, column.RawType, val, result[col],
			)

		} else {
			result[col] = val
		}
	}
	return result
}

func isTextType(typ schema.TableColumn) bool {
	switch typ.Type {
	case schema.TYPE_STRING:
		return true
	default:
		return false
	}
}

func isIntType(typ schema.TableColumn) bool {
	return typ.Type == schema.TYPE_NUMBER || typ.Type == schema.TYPE_MEDIUM_INT
}

func isFloatType(typ schema.TableColumn) bool {
	return typ.Type == schema.TYPE_FLOAT || typ.Type == schema.TYPE_DECIMAL
}

func isDateType(typ schema.TableColumn) bool {
	return typ.Type == schema.TYPE_DATE
}

func isDateTimeType(typ schema.TableColumn) bool {
	switch typ.Type {
	case schema.TYPE_DATETIME, schema.TYPE_TIMESTAMP:
		return true
	default:
		return false
	}
}

func isBooleanType(typ schema.TableColumn) bool {
	return typ.Type == schema.TYPE_NUMBER && typ.RawType == "tinyint(1)"
}

func isBitType(typ schema.TableColumn) bool {
	return typ.Type == schema.TYPE_BIT
}

func getColumnNames(e *canal.RowsEvent) []string {
	columns := make([]string, len(e.Table.Columns))
	for i, col := range e.Table.Columns {
		columns[i] = col.Name
	}
	return columns
}

func loadPosition() (mysql.Position, error) {
	var pos BinlogPosition

	if redisClient != nil {
		data, err := redisClient.Get(redisCtx, redisPositionKey).Result()
		if err == nil {
			err = json.Unmarshal([]byte(data), &pos)
			if err == nil {
				log.Infof("Loaded position from Redis: %+v\n", pos)
				return mysql.Position{Name: pos.Name, Pos: pos.Pos}, nil
			}
			log.Warningln("Error unmarshaling Redis position, falling back to file:", err)
		} else {
			log.Warningln("Redis position not found or error:", err)
		}
	}

	fileData, err := os.ReadFile(positionFile)
	if err != nil {
		log.Warningln("No saved position found, starting from scratch.")
		return mysql.Position{Name: "", Pos: 4}, nil
	}
	if err := json.Unmarshal(fileData, &pos); err != nil {
		log.Errorln("Error loading position from file:", err)
		return mysql.Position{Name: "", Pos: 4}, nil
	}
	log.Infof("Loaded position from file: %+v\n", pos)
	return mysql.Position{Name: pos.Name, Pos: pos.Pos}, nil
}

func savePosition(pos mysql.Position) {
	posData := BinlogPosition{Name: pos.Name, Pos: pos.Pos}
	jsonData, _ := json.Marshal(posData)

	if redisClient != nil {
		err := redisClient.Set(redisCtx, redisPositionKey, jsonData, 0).Err()
		if err != nil {
			log.Errorln("Error saving position to Redis:", err)
		} else {
			log.Tracef("Saved position to Redis: %+v\n", pos)
		}
	}

	err := os.WriteFile(positionFile, jsonData, 0644)
	if err != nil {
		log.Errorln("Error writing position to file:", err)
	} else {
		log.Tracef("Saved position to file: %+v\n", pos)
	}
}

func getGroupForTable(table string) string {
	return tableToGroup[table]
}

// Function to load table groups from .yaml file
func loadTableGroups(configPath string) {
	fileData, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Failed to read table group config: %v", err)
	}
	tableGroups = make(map[string][]string)
	tableToGroup = make(map[string]string)

	if err := yaml.Unmarshal(fileData, &tableGroups); err != nil {
		log.Fatalf("Invalid YAML config: %v", err)
	}

	for group, tables := range tableGroups {
		for _, table := range tables {
			tableToGroup[table] = group
		}
	}

	log.Printf("Loaded table groups: %+v\n", tableGroups)
}

func loadEnv() {
	err := godotenv.Load()
	if err != nil {
		log.Warningln("Error loading .env file, proceeding with environment variables")
	}

	logLevel = getEnv("LOG_LEVEL", "info")

	positionFile = getEnv("POSITION_FILE", "./storage/binlog_position.json")
	postURL = getEnv("POST_URL", "http://localhost:8080")
	configPath := getEnv("TABLE_GROUPS_FILE", "./config/table_groups.yaml")
	loadTableGroups(configPath)

	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")
	redisClient = redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
	})

	_, err = redisClient.Ping(redisCtx).Result()
	if err != nil {
		log.Warningln("Warning: Failed to connect to Redis, fallback to file only:", err)
		redisClient = nil
	} else {
		log.Infof("Connected to Redis at %s\n", redisAddr)
	}
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	value := getEnv(key, "")
	if value == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		log.Errorf("Error parsing %s as int, using default value: %v\n", key, defaultValue)
		return defaultValue
	}
	return intValue
}

func startRedisQueueWorker() {
	log.Infoln("Starting Redis queue worker...")
	for {
		if redisClient == nil {
			log.Warningln("Redis client not initialized. Retrying in 5 seconds...")
			time.Sleep(5 * time.Second)
			continue
		}

		result, err := redisClient.BLPop(redisCtx, 0, redisQueueKey).Result()
		if err != nil {
			log.Errorln("Error reading from Redis queue:", err)
			continue
		}

		if len(result) < 2 {
			continue
		}

		data := result[1]
		tableName := extractTableNameFromPayload(data)
		group := getGroupForTable(tableName)
		if group == "" {
			log.Debugf("Skipping ungrouped table from Redis queue: %s\n", tableName)
			continue
		}
		tableURL := postURL + "/" + group
		log.Infof("Sending HTTP POST request to %s with payload: %s\n", tableURL, data)
		resp, err := http.Post(tableURL, "application/json", bytes.NewBuffer([]byte(data)))
		if err != nil {
			log.Errorln("Error sending HTTP POST:", err)
			continue
		}
		_ = resp.Body.Close()
		log.Debugf("Successfully sent queued event to %s\n", tableURL)
	}
}

func extractTableNameFromPayload(data string) string {
	var payload map[string]interface{}
	err := json.Unmarshal([]byte(data), &payload)
	if err != nil {
		log.Errorln("Error unmarshaling event data:", err)
		return "unknown_table"
	}
	source := payload["source"].(map[string]interface{})
	return source["table"].(string)
}

func main() {
	loadEnv()

	switch strings.ToLower(logLevel) {
	case "trace":
		log.SetLevel(logrus.TraceLevel)
	case "debug":
		log.SetLevel(logrus.DebugLevel)
	case "info":
		fallthrough
	case "warn":
		log.SetLevel(logrus.WarnLevel)
	case "error":
		log.SetLevel(logrus.ErrorLevel)
	default:
		log.SetLevel(logrus.InfoLevel)
	}

	log.SetFormatter(&logrus.TextFormatter{
		DisableSorting: true,
		FullTimestamp:  true,
		ForceColors:    true,
		PadLevelText:   true,
	})

	log.Infoln("Starting CDC...")

	cfg := canal.NewDefaultConfig()
	cfg.Addr = getEnv("DB_ADDR", "127.0.0.1:3306")
	cfg.User = getEnv("DB_USER", "root")
	cfg.Password = getEnv("DB_PASSWORD", "")
	cfg.Flavor = getEnv("DB_FLAVOR", "mysql")
	cfg.ServerID = uint32(getEnvInt("SERVER_ID", 1001))
	cfg.Dump.TableDB = ""
	cfg.Dump.ExecutionPath = ""

	c, err := canal.NewCanal(cfg)
	if err != nil {
		log.Fatal("Error initializing canal:", err)
	}

	c.SetEventHandler(&eventHandler{})

	pos, err := loadPosition()
	if err != nil {
		log.Warningln("Could not load position:", err)
		pos = mysql.Position{Name: "", Pos: 4}
	}

	go startRedisQueueWorker()

	if err := c.RunFrom(pos); err != nil {
		log.Fatal("Canal run error:", err)
	}

	select {}
}
