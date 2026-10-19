package mask

// BankCard 脱敏银行卡号：仅保留末 4 位（****1234）；不足 4 位则 All。
func BankCard(s string) string {
	digits := digitsOnly(s)
	if len(digits) <= 4 {
		return All(digits)
	}
	return "****" + digits[len(digits)-4:]
}
