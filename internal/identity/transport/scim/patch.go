package scim

// PATCH 请求解析（RFC 7644 §3.5.2）：只支持 add / replace / remove，
// 且 path 限定在 §21.1.4 白名单内；其他 path 返回 400 invalidPath。

import (
	"encoding/json"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/app"
)

// patchRequest 是 PATCH 请求体；同时兼容 Operations / operations 两种写法。
type patchRequest struct {
	Schemas         []string         `json:"schemas"`
	Operations      []patchOperation `json:"Operations"`
	OperationsLower []patchOperation `json:"operations"`
}

// patchOperation 是单条补丁操作。
type patchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

// all 返回全部操作，兼容大小写两种字段名。
func (p patchRequest) all() []patchOperation {
	if len(p.Operations) > 0 {
		return p.Operations
	}
	return p.OperationsLower
}

// memberPathPattern 匹配 members[value eq "x"] 形式的成员定位。
var memberPathPattern = strings.NewReplacer(`"`, "", `'`, "")

// parseUserPatch 把 PATCH 请求转换为 User 增量变更。
func parseUserPatch(req patchRequest) (app.ScimUserPatch, error) {
	patch := app.ScimUserPatch{}
	for _, op := range req.all() {
		action := strings.ToLower(trimSpace(op.Op))
		switch action {
		case "add", "replace", "remove":
		default:
			return patch, &app.ScimError{Status: 400, ScimType: "invalidSyntax",
				Detail: "unsupported patch op: " + op.Op}
		}
		path := normalizePath(op.Path)
		if path == "" {
			if action == "remove" {
				return patch, &app.ScimError{Status: 400, ScimType: "noTarget",
					Detail: "remove requires a path"}
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(op.Value, &obj); err != nil {
				return patch, &app.ScimError{Status: 400, ScimType: "invalidValue",
					Detail: "patch value must be an object when path is omitted"}
			}
			for key, raw := range obj {
				if err := applyUserField(&patch, key, raw, action); err != nil {
					return patch, err
				}
			}
			continue
		}
		if err := applyUserField(&patch, path, op.Value, action); err != nil {
			return patch, err
		}
	}
	return patch, nil
}

// applyUserField 按属性名写入增量；只接受 §21.1.4 白名单属性。
func applyUserField(patch *app.ScimUserPatch, rawPath string, raw json.RawMessage, action string) error {
	path := normalizePath(rawPath)
	switch path {
	case "username":
		if action == "remove" {
			return &app.ScimError{Status: 400, ScimType: "noTarget", Detail: "userName cannot be removed"}
		}
		value, err := decodeString(raw)
		if err != nil {
			return err
		}
		patch.UserName = &value
	case "externalid":
		if action == "remove" {
			return &app.ScimError{Status: 400, ScimType: "noTarget", Detail: "externalId cannot be removed"}
		}
		value, err := decodeString(raw)
		if err != nil {
			return err
		}
		patch.ExternalID = &value
	case "displayname":
		if action == "remove" {
			patch.ClearDisplayName = true
			return nil
		}
		value, err := decodeString(raw)
		if err != nil {
			return err
		}
		patch.DisplayName = &value
	case "name", "name.formatted", "name.givenname", "name.familyname", "name.middlename":
		if action == "remove" {
			patch.ClearDisplayName = true
			return nil
		}
		// displayName 是唯一落库的展示字段：复合 name 取 formatted 或拼 given + family。
		var name nameValue
		if err := json.Unmarshal(raw, &name); err != nil {
			// 单值写法（name.formatted / name.givenName）：直接当展示名。
			value, strErr := decodeString(raw)
			if strErr != nil {
				return strErr
			}
			patch.DisplayName = &value
			return nil
		}
		value := name.displayNameOf()
		patch.DisplayName = &value
	case "active":
		if action == "remove" {
			return &app.ScimError{Status: 400, ScimType: "noTarget", Detail: "active cannot be removed"}
		}
		value, err := decodeBool(raw)
		if err != nil {
			return err
		}
		patch.Active = &value
	case "password":
		return &app.ScimError{Status: 400, ScimType: "mutability",
			Detail: "password is managed by the identity provider, not by SCIM"}
	case "groups":
		return &app.ScimError{Status: 400, ScimType: "mutability",
			Detail: "groups are read-only on User; change Group.members instead"}
	default:
		return &app.ScimError{Status: 400, ScimType: "invalidPath", Detail: "unsupported path: " + rawPath}
	}
	return nil
}

// parseGroupPatch 把 PATCH 请求转换为 Group 增量变更。
func parseGroupPatch(req patchRequest) (app.ScimGroupPatch, error) {
	patch := app.ScimGroupPatch{}
	for _, op := range req.all() {
		action := strings.ToLower(trimSpace(op.Op))
		switch action {
		case "add", "replace", "remove":
		default:
			return patch, &app.ScimError{Status: 400, ScimType: "invalidSyntax",
				Detail: "unsupported patch op: " + op.Op}
		}
		path := normalizePath(op.Path)
		if path == "" {
			if action == "remove" {
				return patch, &app.ScimError{Status: 400, ScimType: "noTarget",
					Detail: "remove requires a path"}
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(op.Value, &obj); err != nil {
				return patch, &app.ScimError{Status: 400, ScimType: "invalidValue",
					Detail: "patch value must be an object when path is omitted"}
			}
			for key, raw := range obj {
				if err := applyGroupField(&patch, key, raw, action); err != nil {
					return patch, err
				}
			}
			continue
		}
		if err := applyGroupField(&patch, path, op.Value, action); err != nil {
			return patch, err
		}
	}
	return patch, nil
}

// applyGroupField 按属性名写入组增量。
func applyGroupField(patch *app.ScimGroupPatch, rawPath string, raw json.RawMessage, action string) error {
	path := normalizePath(rawPath)
	switch {
	case path == "displayname":
		value, err := decodeString(raw)
		if err != nil {
			return err
		}
		if action == "remove" {
			return &app.ScimError{Status: 400, ScimType: "noTarget", Detail: "displayName cannot be removed"}
		}
		patch.DisplayName = &value
		return nil
	case path == "externalid":
		value, err := decodeString(raw)
		if err != nil {
			return err
		}
		if action == "remove" {
			return &app.ScimError{Status: 400, ScimType: "noTarget", Detail: "externalId cannot be removed"}
		}
		patch.ExternalID = &value
		return nil
	case path == "members":
		if action == "remove" {
			// remove members（无过滤）等价于清空成员集合。
			patch.ReplaceMembers = []string{}
			return nil
		}
		values, err := decodeMembers(raw)
		if err != nil {
			return err
		}
		if action == "add" {
			patch.AddMembers = append(patch.AddMembers, values...)
		} else {
			patch.ReplaceMembers = values
		}
		return nil
	case strings.HasPrefix(path, "members["):
		value, err := memberValueFromPath(rawPath)
		if err != nil {
			return err
		}
		switch action {
		case "remove":
			patch.RemoveMembers = append(patch.RemoveMembers, value)
		case "add":
			patch.AddMembers = append(patch.AddMembers, value)
		case "replace":
			// 定位到单个成员后替换：先删再加同值，等价于保持成员不变。
			patch.RemoveMembers = append(patch.RemoveMembers, value)
			patch.AddMembers = append(patch.AddMembers, value)
		}
		return nil
	default:
		return &app.ScimError{Status: 400, ScimType: "invalidPath", Detail: "unsupported path: " + rawPath}
	}
}

// decodeMembers 解析 members 值：支持数组与单个对象两种写法。
func decodeMembers(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, &app.ScimError{Status: 400, ScimType: "invalidValue", Detail: "members value is required"}
	}
	var list []memberValue
	if err := json.Unmarshal(raw, &list); err != nil {
		var single memberValue
		if singleErr := json.Unmarshal(raw, &single); singleErr != nil {
			return nil, &app.ScimError{Status: 400, ScimType: "invalidValue",
				Detail: "members value must be an array of {value}"}
		}
		list = []memberValue{single}
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		value := trimSpace(item.Value)
		if value == "" {
			return nil, &app.ScimError{Status: 400, ScimType: "invalidValue", Detail: "member value is required"}
		}
		out = append(out, value)
	}
	return out, nil
}

// memberValueFromPath 从 `members[value eq "x"]` 提取成员标识。
func memberValueFromPath(rawPath string) (string, error) {
	open := strings.Index(rawPath, "[")
	closeIdx := strings.LastIndex(rawPath, "]")
	if open < 0 || closeIdx <= open {
		return "", &app.ScimError{Status: 400, ScimType: "invalidPath",
			Detail: "member path must be members[value eq \"...\"]"}
	}
	expression := memberPathPattern.Replace(rawPath[open+1 : closeIdx])
	parts := strings.SplitN(expression, " eq ", 2)
	if len(parts) != 2 || !strings.EqualFold(trimSpace(parts[0]), "value") {
		return "", &app.ScimError{Status: 400, ScimType: "invalidPath",
			Detail: "member path must be members[value eq \"...\"]"}
	}
	value := trimSpace(parts[1])
	if value == "" {
		return "", &app.ScimError{Status: 400, ScimType: "invalidValue", Detail: "member value is required"}
	}
	return value, nil
}

// normalizePath 去掉引号与空白并转小写；空串保持为空。
func normalizePath(raw string) string {
	trimmed := trimSpace(raw)
	trimmed = strings.ReplaceAll(trimmed, `"`, "")
	trimmed = strings.ReplaceAll(trimmed, `'`, "")
	return strings.ToLower(trimmed)
}

// decodeString 解析字符串值。
func decodeString(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", &app.ScimError{Status: 400, ScimType: "invalidValue", Detail: "value must be a string"}
	}
	return value, nil
}

// decodeBool 解析布尔值；兼容 "true" / "false" 字符串。
func decodeBool(raw json.RawMessage) (bool, error) {
	var value bool
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if parsed, parseErr := parseBool(text); parseErr == nil {
			return parsed, nil
		}
	}
	return false, &app.ScimError{Status: 400, ScimType: "invalidValue", Detail: "value must be a boolean"}
}
