package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kervanserver/kervan/internal/auth"
)

type userImportRecord struct {
	Row          int    `json:"row,omitempty"`
	Username     string `json:"username"`
	Password     string `json:"password,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	Email        string `json:"email,omitempty"`
	Role         string `json:"role,omitempty"`
	Type         string `json:"type,omitempty"`
	HomeDir      string `json:"home_dir,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
}

type userImportError struct {
	Row      int    `json:"row"`
	Username string `json:"username,omitempty"`
	Error    string `json:"error"`
}

type userImportReport struct {
	Format    string            `json:"format"`
	Total     int               `json:"total"`
	Created   int               `json:"created"`
	Skipped   int               `json:"skipped"`
	Usernames []string          `json:"usernames,omitempty"`
	Errors    []userImportError `json:"errors,omitempty"`
}

type userExportRecord struct {
	Username     string `json:"username"`
	Email        string `json:"email,omitempty"`
	Role         string `json:"role"`
	Type         string `json:"type"`
	HomeDir      string `json:"home_dir"`
	Enabled      bool   `json:"enabled"`
	PasswordHash string `json:"password_hash,omitempty"`
}

func (s *Server) handleUsersImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.isAdminUser(currentUser(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}

	formatHint := strings.TrimSpace(r.URL.Query().Get("format"))
	format, records, err := parseUserImportRequest(r, formatHint)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	report := userImportReport{
		Format: format,
		Total:  len(records),
	}
	for _, record := range records {
		createdUser, createErr := s.createImportedUser(record)
		if createErr != nil {
			report.Skipped++
			report.Errors = append(report.Errors, userImportError{
				Row:      record.Row,
				Username: record.Username,
				Error:    createErr.Error(),
			})
			continue
		}
		report.Created++
		report.Usernames = append(report.Usernames, createdUser.Username)
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleUsersExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.isAdminUser(currentUser(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}

	format, err := normalizeUserDataFormat(r.URL.Query().Get("format"), "users.json")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	includePasswordHashes := parseBoolQuery(r.URL.Query().Get("include_password_hashes"))

	users, err := s.users.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i] == nil {
			return false
		}
		if users[j] == nil {
			return true
		}
		return strings.ToLower(users[i].Username) < strings.ToLower(users[j].Username)
	})

	records := make([]userExportRecord, 0, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		record := userExportRecord{
			Username: user.Username,
			Email:    user.Email,
			Role:     exportRoleForUser(user),
			Type:     string(user.Type),
			HomeDir:  user.HomeDir,
			Enabled:  user.Enabled,
		}
		if includePasswordHashes {
			record.PasswordHash = user.PasswordHash
		}
		records = append(records, record)
	}

	filename := "users." + format
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(records)
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		if err := writeUserExportCSV(w, records, includePasswordHashes); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported format"})
	}
}

func parseUserImportRequest(r *http.Request, formatHint string) (string, []userImportRecord, error) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	switch {
	case strings.Contains(contentType, "multipart/form-data"):
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			return "", nil, err
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return "", nil, errors.New("file is required")
		}
		defer file.Close()

		format, err := normalizeUserDataFormat(formatHint, header.Filename)
		if err != nil {
			return "", nil, err
		}
		records, err := loadUserImportRecords(file, format)
		return format, records, err
	case strings.Contains(contentType, "application/json"):
		records, err := loadUserImportRecords(r.Body, "json")
		return "json", records, err
	case strings.Contains(contentType, "text/csv"), strings.Contains(contentType, "application/csv"):
		records, err := loadUserImportRecords(r.Body, "csv")
		return "csv", records, err
	default:
		format, err := normalizeUserDataFormat(formatHint, "")
		if err != nil {
			return "", nil, errors.New("unsupported import content type")
		}
		records, err := loadUserImportRecords(r.Body, format)
		return format, records, err
	}
}

func loadUserImportRecords(reader io.Reader, format string) ([]userImportRecord, error) {
	switch format {
	case "json":
		var records []userImportRecord
		decoder := json.NewDecoder(reader)
		if err := decoder.Decode(&records); err != nil {
			return nil, err
		}
		for index := range records {
			records[index].Row = index + 1
			records[index].Username = strings.TrimSpace(records[index].Username)
			records[index].Password = strings.TrimSpace(records[index].Password)
			records[index].PasswordHash = strings.TrimSpace(records[index].PasswordHash)
			records[index].Email = strings.TrimSpace(records[index].Email)
			records[index].Role = strings.TrimSpace(records[index].Role)
			records[index].Type = strings.TrimSpace(records[index].Type)
			records[index].HomeDir = strings.TrimSpace(records[index].HomeDir)
		}
		return records, nil
	case "csv":
		csvReader := csv.NewReader(reader)
		rows, err := csvReader.ReadAll()
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, errors.New("csv file is empty")
		}
		header := make(map[string]int, len(rows[0]))
		for idx, col := range rows[0] {
			header[strings.ToLower(strings.TrimSpace(col))] = idx
		}
		records := make([]userImportRecord, 0, len(rows)-1)
		for rowIndex, row := range rows[1:] {
			record := userImportRecord{
				Row:          rowIndex + 2,
				Username:     csvValue(row, header, "username"),
				Password:     csvValue(row, header, "password"),
				PasswordHash: csvValue(row, header, "password_hash"),
				Email:        csvValue(row, header, "email"),
				Role:         csvValue(row, header, "role"),
				Type:         csvValue(row, header, "type"),
				HomeDir:      csvValue(row, header, "home_dir"),
			}
			enabled, err := parseOptionalBool(csvValue(row, header, "enabled"))
			if err != nil {
				return nil, fmt.Errorf("row %d: %w", record.Row, err)
			}
			record.Enabled = enabled
			records = append(records, record)
		}
		return records, nil
	default:
		return nil, fmt.Errorf("unsupported import format: %s", format)
	}
}

func (s *Server) createImportedUser(record userImportRecord) (*auth.User, error) {
	username := strings.TrimSpace(record.Username)
	if username == "" {
		return nil, errors.New("username is required")
	}
	userType, err := resolveImportedUserType(record.Role, record.Type)
	if err != nil {
		return nil, err
	}
	homeDir := strings.TrimSpace(record.HomeDir)
	if homeDir == "" {
		homeDir = "/"
	}

	var user *auth.User
	switch {
	case record.PasswordHash != "":
		user = &auth.User{
			Username:     username,
			PasswordHash: record.PasswordHash,
			Email:        strings.TrimSpace(record.Email),
			Type:         userType,
			HomeDir:      homeDir,
			Enabled:      true,
			Permissions:  auth.DefaultUserPermissions(),
		}
		if err := s.users.Create(user); err != nil {
			return nil, err
		}
	case record.Password != "":
		user, err = s.auth.CreateUser(username, record.Password, homeDir, userType == auth.UserTypeAdmin)
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("password or password_hash is required")
	}

	needsUpdate := false
	if user.Email != strings.TrimSpace(record.Email) {
		user.Email = strings.TrimSpace(record.Email)
		needsUpdate = true
	}
	if user.Type != userType {
		user.Type = userType
		needsUpdate = true
	}
	if record.Enabled != nil && user.Enabled != *record.Enabled {
		user.Enabled = *record.Enabled
		needsUpdate = true
	}
	if user.HomeDir != homeDir {
		user.HomeDir = homeDir
		needsUpdate = true
	}
	if needsUpdate {
		if err := s.users.Update(user); err != nil {
			return nil, err
		}
	}
	return user, nil
}

func normalizeUserDataFormat(formatHint, fileName string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(formatHint))
	if format == "" || format == "auto" {
		switch strings.ToLower(filepath.Ext(strings.TrimSpace(fileName))) {
		case ".json":
			return "json", nil
		case ".csv":
			return "csv", nil
		default:
			if strings.TrimSpace(fileName) == "" {
				return "", errors.New("format is required")
			}
			return "", fmt.Errorf("unable to detect format for %q", fileName)
		}
	}
	switch format {
	case "json", "csv":
		return format, nil
	default:
		return "", fmt.Errorf("unsupported format: %s", formatHint)
	}
}

func resolveImportedUserType(role, rawType string) (auth.UserType, error) {
	value := strings.ToLower(strings.TrimSpace(role))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(rawType))
	}
	switch value {
	case "", "user", "virtual":
		return auth.UserTypeVirtual, nil
	case "admin":
		return auth.UserTypeAdmin, nil
	default:
		return "", fmt.Errorf("unsupported role/type: %s", value)
	}
}

func exportRoleForUser(user *auth.User) string {
	if user != nil && user.Type == auth.UserTypeAdmin {
		return "admin"
	}
	return "user"
}

func writeUserExportCSV(w io.Writer, records []userExportRecord, includePasswordHashes bool) error {
	writer := csv.NewWriter(w)
	header := []string{"username", "email", "role", "type", "home_dir", "enabled"}
	if includePasswordHashes {
		header = append(header, "password_hash")
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, record := range records {
		row := []string{
			record.Username,
			record.Email,
			record.Role,
			record.Type,
			record.HomeDir,
			fmt.Sprintf("%t", record.Enabled),
		}
		if includePasswordHashes {
			row = append(row, record.PasswordHash)
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func csvValue(row []string, header map[string]int, column string) string {
	idx, ok := header[column]
	if !ok || idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func parseOptionalBool(raw string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return nil, nil
	case "1", "true", "yes", "y":
		value := true
		return &value, nil
	case "0", "false", "no", "n":
		value := false
		return &value, nil
	default:
		return nil, fmt.Errorf("invalid enabled value %q", raw)
	}
}

func parseBoolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
