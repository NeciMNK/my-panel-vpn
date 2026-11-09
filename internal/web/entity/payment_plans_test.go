package entity

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePaymentPlans(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []PaymentPlan
		wantErr string
	}{
		{name: "empty offers one month", in: "", want: []PaymentPlan{{Months: 1}}},
		{
			name: "unsorted with spaces",
			in:   " 12:20, 3:10 ,6:15",
			want: []PaymentPlan{{Months: 1}, {3, 10}, {6, 15}, {12, 20}},
		},
		{name: "wrong separator", in: "3-10", wantErr: "is not months:percent"},
		{name: "one month cannot be discounted", in: "1:5", wantErr: "months must be 2-36"},
		{name: "free term", in: "3:100", wantErr: "percent 0-99"},
		{name: "duplicate term", in: "3:10,3:20", wantErr: "listed twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePaymentPlans(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParsePaymentPlans(%q) = %v, %v; want error containing %q", tc.in, got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePaymentPlans(%q): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParsePaymentPlans(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// 3 months at 10% off 300 is 810, not 900 or 270.
func TestPaymentPlanTotalAppliesDiscountToWholeTerm(t *testing.T) {
	if got := (PaymentPlan{Months: 3, DiscountPercent: 10}).Total(300); got != 810 {
		t.Errorf("Total = %d, want 810", got)
	}
}
