package bulkimport

import (
	"encoding/csv"
	"io"
	"strings"
)

type LeadRow struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Company   string `json:"company"`
	Source    string `json:"source"`
}

type ImportResult struct {
	TotalProcessed int       `json:"total_processed"`
	Successful     int       `json:"successful"`
	Failed         int       `json:"failed"`
	Leads          []LeadRow `json:"leads"`
	Errors         []string  `json:"errors"`
}

func ParseLeadsCSV(r io.Reader) (ImportResult, error) {
	reader := csv.NewReader(r)
	var result ImportResult

	header, err := reader.Read()
	if err != nil {
		return result, err
	}

	fieldMap := make(map[string]int)
	for i, col := range header {
		fieldMap[strings.ToLower(strings.TrimSpace(col))] = i
	}

	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			result.Failed++
			continue
		}

		result.TotalProcessed++

		getVal := func(name string) string {
			if idx, ok := fieldMap[name]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
			return ""
		}

		lead := LeadRow{
			FirstName: getVal("first_name"),
			LastName:  getVal("last_name"),
			Email:     getVal("email"),
			Phone:     getVal("phone"),
			Company:   getVal("company"),
			Source:    getVal("source"),
		}

		if lead.FirstName == "" && lead.LastName == "" && lead.Email == "" {
			result.Errors = append(result.Errors, "row missing required identification fields (name or email)")
			result.Failed++
			continue
		}

		result.Leads = append(result.Leads, lead)
		result.Successful++
	}

	return result, nil
}
