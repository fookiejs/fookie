package compiler

import (
	"fmt"
	"strings"

	"github.com/fookiejs/fookie/pkg/ast"
)

func (sg *SQLGenerator) BuildWhereClause(model *ast.Model, filter map[string]interface{}, paramStart int) (string, []interface{}, int, error) {
	if len(filter) == 0 {
		return "", nil, paramStart, nil
	}
	return sg.buildWhereMap(model, filter, paramStart)
}

func (sg *SQLGenerator) fieldMeta(model *ast.Model, name string) (col string, ft ast.FieldType, ok bool) {
	switch name {
	case "id":
		return "id", ast.TypeUUID, true
	case "status":
		return "status", ast.TypeString, true
	case "createdAt":
		return "created_at", ast.TypeTimestamp, true
	case "updatedAt":
		return "updated_at", ast.TypeTimestamp, true
	default:
		for _, f := range model.Fields {
			if f.Name == name {
				return snake(f.Name), f.Type, true
			}
		}
	}
	return "", "", false
}

func (sg *SQLGenerator) buildWhereMap(model *ast.Model, m map[string]interface{}, paramStart int) (string, []interface{}, int, error) {
	var parts []string
	var allArgs []interface{}
	idx := paramStart

	flushField := func(k string, v interface{}) error {
		fm, ok := v.(map[string]interface{})
		if !ok {
			return fmt.Errorf("field %q filter must be an object", k)
		}
		col, ft, ok := sg.fieldMeta(model, k)
		if !ok {
			return fmt.Errorf("unknown filter field %q", k)
		}
		quoted := fmt.Sprintf(`"%s"`, col)
		s, a, next, err := sg.buildFieldOps(quoted, ft, fm, idx)
		if err != nil {
			return err
		}
		if s != "" {
			parts = append(parts, s)
			allArgs = append(allArgs, a...)
			idx = next
		}
		return nil
	}

	for k, v := range m {
		switch k {
		case "AND":
			arr, ok := v.([]interface{})
			if !ok {
				return "", nil, paramStart, fmt.Errorf("AND expects a list")
			}
			var subParts []string
			for _, item := range arr {
				child, ok := item.(map[string]interface{})
				if !ok {
					return "", nil, paramStart, fmt.Errorf("AND item must be an object")
				}
				s, a, next, err := sg.buildWhereMap(model, child, idx)
				if err != nil {
					return "", nil, paramStart, err
				}
				idx = next
				allArgs = append(allArgs, a...)
				if s == "" {
					continue
				}
				subParts = append(subParts, "("+s+")")
			}
			if len(subParts) > 0 {
				parts = append(parts, "("+strings.Join(subParts, " AND ")+")")
			}

		case "OR":
			arr, ok := v.([]interface{})
			if !ok {
				return "", nil, paramStart, fmt.Errorf("OR expects a list")
			}
			var subParts []string
			for _, item := range arr {
				child, ok := item.(map[string]interface{})
				if !ok {
					return "", nil, paramStart, fmt.Errorf("OR item must be an object")
				}
				s, a, next, err := sg.buildWhereMap(model, child, idx)
				if err != nil {
					return "", nil, paramStart, err
				}
				idx = next
				allArgs = append(allArgs, a...)
				if s == "" {
					subParts = append(subParts, "FALSE")
					continue
				}
				subParts = append(subParts, "("+s+")")
			}
			if len(subParts) > 0 {
				parts = append(parts, "("+strings.Join(subParts, " OR ")+")")
			}

		case "NOT":
			child, ok := v.(map[string]interface{})
			if !ok {
				return "", nil, paramStart, fmt.Errorf("NOT expects an object")
			}
			s, a, next, err := sg.buildWhereMap(model, child, idx)
			if err != nil {
				return "", nil, paramStart, err
			}
			idx = next
			allArgs = append(allArgs, a...)
			if s != "" {
				parts = append(parts, "NOT ("+s+")")
			}

		default:
			if err := flushField(k, v); err != nil {
				return "", nil, paramStart, err
			}
		}
	}

	if len(parts) == 0 {
		return "", allArgs, idx, nil
	}
	return strings.Join(parts, " AND "), allArgs, idx, nil
}

func likePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return "%" + s + "%"
}

func prefixPattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s + "%"
}

func suffixPattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return "%" + s
}

func (sg *SQLGenerator) buildFieldOps(col string, ft ast.FieldType, ops map[string]interface{}, paramStart int) (string, []interface{}, int, error) {
	var parts []string
	var args []interface{}
	idx := paramStart

	isUUIDish := ft == ast.TypeID || ft == ast.TypeRelation || ft == ast.TypeUUID
	isNum := ft == ast.TypeNumber
	isBool := ft == ast.TypeBoolean
	isTime := ft == ast.TypeTimestamp
	isDate := ft == ast.TypeDate

	appendPred := func(fragment string, vals ...interface{}) {
		parts = append(parts, fragment)
		args = append(args, vals...)
		idx += len(vals)
	}

	for opName, raw := range ops {
		switch opName {
		case "eq":
			if isUUIDish {
				appendPred(col+fmt.Sprintf(` = $%d`, idx), raw)
			} else if isNum {
				appendPred(col+fmt.Sprintf(` = $%d`, idx), toFloat64(raw))
			} else if isBool {
				appendPred(col+fmt.Sprintf(` = $%d`, idx), toBool(raw))
			} else if isTime {
				appendPred(col+fmt.Sprintf(` = $%d::timestamptz`, idx), raw)
			} else if isDate {
				appendPred(col+fmt.Sprintf(` = $%d::date`, idx), raw)
			} else {
				appendPred(col+fmt.Sprintf(` = $%d`, idx), raw)
			}

		case "neq":
			if isUUIDish {
				appendPred(col+fmt.Sprintf(` <> $%d`, idx), raw)
			} else if isNum {
				appendPred(col+fmt.Sprintf(` <> $%d`, idx), toFloat64(raw))
			} else if isBool {
				appendPred(col+fmt.Sprintf(` <> $%d`, idx), toBool(raw))
			} else if isTime {
				appendPred(col+fmt.Sprintf(` <> $%d::timestamptz`, idx), raw)
			} else if isDate {
				appendPred(col+fmt.Sprintf(` <> $%d::date`, idx), raw)
			} else {
				appendPred(col+fmt.Sprintf(` <> $%d`, idx), raw)
			}

		case "gt", "gte", "lt", "lte":
			sop := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[opName]
			if isNum {
				appendPred(col+fmt.Sprintf(` `+sop+` $%d`, idx), toFloat64(raw))
			} else if isTime {
				appendPred(col+fmt.Sprintf(` `+sop+` $%d::timestamptz`, idx), raw)
			} else if isDate {
				appendPred(col+fmt.Sprintf(` `+sop+` $%d::date`, idx), raw)
			} else {
				appendPred(col+fmt.Sprintf(` `+sop+` $%d`, idx), raw)
			}

		case "contains":
			if isNum || isBool || isUUIDish {
				return "", nil, paramStart, fmt.Errorf("contains not supported for this field type")
			}
			appendPred(col+fmt.Sprintf(` ILIKE $%d ESCAPE '\'`, idx), likePattern(fmt.Sprint(raw)))

		case "startsWith":
			if isNum || isBool || isUUIDish {
				return "", nil, paramStart, fmt.Errorf("startsWith not supported for this field type")
			}
			appendPred(col+fmt.Sprintf(` ILIKE $%d ESCAPE '\'`, idx), prefixPattern(fmt.Sprint(raw)))

		case "endsWith":
			if isNum || isBool || isUUIDish {
				return "", nil, paramStart, fmt.Errorf("endsWith not supported for this field type")
			}
			appendPred(col+fmt.Sprintf(` ILIKE $%d ESCAPE '\'`, idx), suffixPattern(fmt.Sprint(raw)))

		case "in":
			sl, err := sliceAny(raw)
			if err != nil {
				return "", nil, paramStart, err
			}
			if len(sl) == 0 {
				appendPred(`FALSE`)
				continue
			}
			ph := make([]string, len(sl))
			for i := range sl {
				var v interface{}
				if isNum {
					v = toFloat64(sl[i])
				} else {
					v = sl[i]
				}
				ph[i] = fmt.Sprintf("$%d", idx)
				args = append(args, v)
				idx++
			}
			parts = append(parts, col+` IN (`+strings.Join(ph, ", ")+`)`)

		case "notIn":
			sl, err := sliceAny(raw)
			if err != nil {
				return "", nil, paramStart, err
			}
			if len(sl) == 0 {
				appendPred(`TRUE`)
				continue
			}
			ph := make([]string, len(sl))
			for i := range sl {
				var v interface{}
				if isNum {
					v = toFloat64(sl[i])
				} else {
					v = sl[i]
				}
				ph[i] = fmt.Sprintf("$%d", idx)
				args = append(args, v)
				idx++
			}
			parts = append(parts, col+` NOT IN (`+strings.Join(ph, ", ")+`)`)

		default:
			return "", nil, paramStart, fmt.Errorf("unknown filter operator %q", opName)
		}
	}

	if len(parts) == 0 {
		return "", nil, paramStart, nil
	}
	return strings.Join(parts, " AND "), args, idx, nil
}

func toFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float32:
		return float64(n)
	default:
		return 0
	}
}

func toBool(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func sliceAny(raw interface{}) ([]interface{}, error) {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected array")
	}
	return arr, nil
}

func (sg *SQLGenerator) CompileBulkUpdate(model *ast.Model, patch map[string]interface{}, filter map[string]interface{}) (string, []interface{}, error) {
	if len(patch) == 0 {
		return "", nil, fmt.Errorf("empty patch")
	}
	if len(filter) == 0 {
		return "", nil, fmt.Errorf("where filter is required")
	}
	table := snake(model.Name)
	var keys []string
	for k := range patch {
		keys = append(keys, k)
	}
	setCount := len(keys)
	whereSQL, whereArgs, _, err := sg.BuildWhereClause(model, filter, setCount+1)
	if err != nil {
		return "", nil, err
	}
	if whereSQL == "" {
		return "", nil, fmt.Errorf("where filter is required")
	}
	var sets []string
	args := make([]interface{}, 0, setCount+len(whereArgs))
	for i, k := range keys {
		sets = append(sets, fmt.Sprintf(`"%s" = $%d`, snake(k), i+1))
		args = append(args, patch[k])
	}
	args = append(args, whereArgs...)
	sql := fmt.Sprintf(
		`UPDATE "%s" SET %s, "updated_at" = NOW() WHERE "deleted_at" IS NULL AND (%s)`,
		table, strings.Join(sets, ", "), whereSQL,
	)
	return sql, args, nil
}

func (sg *SQLGenerator) CompileBulkSoftDelete(model *ast.Model, filter map[string]interface{}) (string, []interface{}, error) {
	if len(filter) == 0 {
		return "", nil, fmt.Errorf("where filter is required")
	}
	table := snake(model.Name)
	whereSQL, whereArgs, _, err := sg.BuildWhereClause(model, filter, 1)
	if err != nil {
		return "", nil, err
	}
	if whereSQL == "" {
		return "", nil, fmt.Errorf("where filter is required")
	}
	sql := fmt.Sprintf(
		`UPDATE "%s" SET "deleted_at" = NOW(), "updated_at" = NOW() WHERE "deleted_at" IS NULL AND (%s)`,
		table, whereSQL,
	)
	return sql, whereArgs, nil
}
