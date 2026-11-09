package main

import (
	"encoding/json"
	"errors"
	"math"
	"time"
)

type limit struct {
	Remaining *float64 `json:"remaining_percent"`
	ResetsAt  string   `json:"resets_at"`
}
type balance struct {
	Included *struct {
		Session   *limit   `json:"session"`
		Weekly    *limit   `json:"weekly"`
		Balance   *float64 `json:"balance_usd"`
		Allowance *float64 `json:"allowance_usd"`
		Period    struct {
			Until string `json:"until"`
		} `json:"period"`
	} `json:"included"`
	Purchased *struct {
		Balance *float64 `json:"balance_usd"`
	} `json:"purchased"`
}

func normalizeBalance(raw []byte) (quotaResponse, error) {
	var b balance
	if err := json.Unmarshal(raw, &b); err != nil || b.Included == nil {
		return quotaResponse{}, errors.New("Ollama returned an invalid balance response")
	}
	result := quotaResponse{}
	g := group{DisplayName: "Ollama Cloud"}
	i := b.Included
	if i.Session != nil || i.Weekly != nil {
		if i.Session == nil || i.Weekly == nil {
			return result, errors.New("Ollama returned incomplete session/weekly limits")
		}
		for _, entry := range []struct {
			window, label string
			value         *limit
		}{{"5h", "5-hour usage", i.Session}, {"weekly", "Weekly usage", i.Weekly}} {
			v := entry.value
			if v.Remaining == nil || !finite(*v.Remaining) || *v.Remaining < 0 || *v.Remaining > 100 {
				return quotaResponse{}, errors.New("Ollama returned an invalid remaining percentage")
			}
			reset, err := resetTime(v.ResetsAt)
			if err != nil {
				return quotaResponse{}, err
			}
			g.Buckets = append(g.Buckets, bucket{Window: entry.window, Description: entry.label, RemainingFraction: *v.Remaining / 100, ResetTime: reset})
		}
		result.Subscription = &subscription{Plan: "Session / weekly limits"}
	} else {
		if i.Balance == nil || i.Allowance == nil || !finite(*i.Balance) || !finite(*i.Allowance) || *i.Allowance < 0 {
			return result, errors.New("Ollama returned an unsupported balance schema")
		}
		reset, err := resetTime(i.Period.Until)
		if err != nil {
			return quotaResponse{}, err
		}
		result.Subscription = &subscription{Plan: "Credit-based plan"}
		result.Summary = append(result.Summary, money("included_balance", "Included credits remaining", *i.Balance), money("included_allowance", "Included allowance", *i.Allowance))
		if *i.Allowance > 0 {
			g.Buckets = append(g.Buckets, bucket{Window: "monthly", Description: "Included credits", RemainingFraction: math.Max(0, math.Min(1, *i.Balance / *i.Allowance)), ResetTime: reset})
		}
	}
	if len(g.Buckets) > 0 {
		result.Groups = []group{g}
	}
	if b.Purchased != nil && b.Purchased.Balance != nil {
		if !finite(*b.Purchased.Balance) {
			return quotaResponse{}, errors.New("Ollama returned an invalid purchased balance")
		}
		result.Summary = append(result.Summary, money("purchased_balance", "Purchased credits", *b.Purchased.Balance))
	}
	return result, nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func money(key, label string, value float64) metric {
	return metric{Key: key, Label: label, Value: value, Format: "currency", Currency: "USD"}
}
func resetTime(value string) (string, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", errors.New("Ollama returned an invalid reset timestamp")
	}
	return t.UTC().Format(time.RFC3339), nil
}
