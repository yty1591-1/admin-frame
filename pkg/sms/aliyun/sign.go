package aliyun

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G401,G505 -- 阿里云 Dysmsapi 签名算法固定为 HMAC-SHA1，非通用哈希用途。
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	apiAction        = "SendSms"
	apiFormat        = "JSON"
	apiVersion       = "2017-05-25"
	signatureMethod  = "HMAC-SHA1"
	signatureVersion = "1.0"
	timestampLayout  = "2006-01-02T15:04:05Z"
	signatureParam   = "Signature"
	nonceBytes       = 16
	outIDMaxRunes    = 128
	maxResponseBytes = 64 << 10
	defaultRegion    = "cn-hangzhou"
	defaultEndpoint  = "https://dysmsapi.aliyuncs.com"
)

// percentEncode 按阿里云 RPC 签名规范编码：RFC 3986 + 空格为 %20、星号为 %2A、波浪号保留。
func percentEncode(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

// canonicalQuery 按键排序并编码，用于构造待签名字符串。
func canonicalQuery(params url.Values) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, key := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(percentEncode(key))
		b.WriteByte('=')
		b.WriteString(percentEncode(params.Get(key)))
	}
	return b.String()
}

// stringToSign 返回 StringToSign：GET&%2F&percentEncode(canonicalQuery)。
func stringToSign(params url.Values) string {
	return "GET&%2F&" + percentEncode(canonicalQuery(params))
}

// sign 计算 Base64(HMAC-SHA1(secret+"&", stringToSign))。
func sign(params url.Values, secret string) string {
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign(params)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func newNonce() (string, error) {
	var buf [nonceBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func newTimestamp(now time.Time) string {
	return now.UTC().Format(timestampLayout)
}
