package entity

import (
	"slices"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// PaymentPlan is one term a client can pay for: Months at DiscountPercent off.
type PaymentPlan struct {
	Months          int
	DiscountPercent int
}

// Total is the term's price for a monthly price, rounded to the nearest unit.
func (p PaymentPlan) Total(monthlyPrice int) int {
	return (monthlyPrice*p.Months*(100-p.DiscountPercent) + 50) / 100
}

// ParsePaymentPlans reads "months:percent" pairs such as "3:10, 6:15"; the result
// always starts with the undiscounted single month and is sorted by months.
func ParsePaymentPlans(discounts string) ([]PaymentPlan, error) {
	plans := []PaymentPlan{{Months: 1}}
	for raw := range strings.SplitSeq(discounts, ",") {
		pair := strings.TrimSpace(raw)
		if pair == "" {
			continue
		}
		monthsText, percentText, ok := strings.Cut(pair, ":")
		months, mErr := strconv.Atoi(strings.TrimSpace(monthsText))
		percent, pErr := strconv.Atoi(strings.TrimSpace(percentText))
		if !ok || mErr != nil || pErr != nil {
			return nil, common.NewErrorf("payment discount %q is not months:percent", pair)
		}
		if months < 2 || months > 36 || percent < 0 || percent > 99 {
			return nil, common.NewErrorf("payment discount %q: months must be 2-36 and percent 0-99", pair)
		}
		if slices.ContainsFunc(plans, func(p PaymentPlan) bool { return p.Months == months }) {
			return nil, common.NewErrorf("payment discount for %d months is listed twice", months)
		}
		plans = append(plans, PaymentPlan{Months: months, DiscountPercent: percent})
	}
	slices.SortFunc(plans, func(a, b PaymentPlan) int { return a.Months - b.Months })
	return plans, nil
}
