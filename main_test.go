package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockSESEmailSender is a mock implementation of SESEmailSender interface
type MockSESEmailSender struct {
	mock.Mock
}

func (m *MockSESEmailSender) SendEmail(ctx context.Context, params *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	args := m.Called(ctx, params, optFns)
	return args.Get(0).(*sesv2.SendEmailOutput), args.Error(1)
}

// setGlobals points the package globals at test values and restores them when
// the test ends, so tests don't depend on run order.
func setGlobals(t *testing.T, dry bool) {
	t.Helper()
	oldDry, oldFrom, oldDir, oldDelay, oldNow := dryRun, from, auditDir, sendDelay, now
	t.Cleanup(func() {
		dryRun, from, auditDir, sendDelay, now = oldDry, oldFrom, oldDir, oldDelay, oldNow
	})
	dryRun = dry
	from = "sender@example.com"
	auditDir = filepath.Join(t.TempDir(), ".audit")
	sendDelay = 0
	now = func() time.Time { return time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC) }
}

func TestBreaksRules(t *testing.T) {
	couples := map[string]Participant{
		"Alice":   {Email: "alice@example.com", Partner: "Dave"},
		"Bob":     {Email: "bob@example.com", Partner: "Eve"},
		"Charlie": {Email: "charlie@example.com", Partner: "Frank"},
	}
	tests := []struct {
		name         string
		recipients   []string
		participants map[string]Participant
		past         map[string]string
		expected     bool
	}{
		{
			name:         "No matches",
			recipients:   []string{"Charlie", "Alice", "Bob"},
			participants: couples,
		},
		{
			name:         "Match with self",
			recipients:   []string{"Alice", "Charlie", "Bob"},
			participants: couples,
			expected:     true,
		},
		{
			name:         "Match with self, different case",
			recipients:   []string{"alice", "Charlie", "Bob"},
			participants: couples,
			expected:     true,
		},
		{
			name:         "Match with partner",
			recipients:   []string{"Charlie", "Eve", "Bob"},
			participants: couples,
			expected:     true,
		},
		{
			name:       "Fixed recipient satisfied",
			recipients: []string{"Charlie", "Alice", "Bob"},
			participants: map[string]Participant{
				"Alice":   {Email: "alice@example.com", Fixed: "charlie"},
				"Bob":     {Email: "bob@example.com"},
				"Charlie": {Email: "charlie@example.com"},
			},
		},
		{
			name:       "Fixed recipient missed",
			recipients: []string{"Bob", "Charlie", "Alice"},
			participants: map[string]Participant{
				"Alice":   {Email: "alice@example.com", Fixed: "Charlie"},
				"Bob":     {Email: "bob@example.com"},
				"Charlie": {Email: "charlie@example.com"},
			},
			expected: true,
		},
		{
			name:         "Same as last year",
			recipients:   []string{"Charlie", "Alice", "Bob"},
			participants: couples,
			past:         map[string]string{"Bob": "Alice"},
			expected:     true,
		},
		{
			name:         "Different from last year",
			recipients:   []string{"Charlie", "Alice", "Bob"},
			participants: couples,
			past:         map[string]string{"Bob": "Charlie"},
		},
		{
			name:       "Fixed recipient wins over last year",
			recipients: []string{"Charlie", "Alice", "Bob"},
			participants: map[string]Participant{
				"Alice":   {Email: "alice@example.com", Fixed: "Charlie"},
				"Bob":     {Email: "bob@example.com"},
				"Charlie": {Email: "charlie@example.com"},
			},
			past: map[string]string{"Alice": "Charlie"},
		},
	}

	givers := []string{"Alice", "Bob", "Charlie"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, breaksRules(givers, tt.recipients, tt.participants, tt.past))
		})
	}
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name:   "valid",
			config: `{"participants": {"A": {"email": "a@example.com", "partner": "B"}, "B": {"email": "b@example.com", "partner": "A"}, "C": {"email": "c@example.com", "partner": "NA"}}}`,
		},
		{
			name:    "malformed json",
			config:  `{"participants": `,
			wantErr: "parsing config",
		},
		{
			name:    "unknown field",
			config:  `{"participants": {"A": {"email": "a@example.com", "parnter": "B"}, "B": {"email": "b@example.com"}}}`,
			wantErr: "unknown field",
		},
		{
			name:    "old list format",
			config:  `{"A": ["a@example.com", ""], "B": ["b@example.com", ""]}`,
			wantErr: "unknown field",
		},
		{
			name:    "one participant",
			config:  `{"participants": {"A": {"email": "a@example.com"}}}`,
			wantErr: "at least 2 participants",
		},
		{
			name:    "missing email",
			config:  `{"participants": {"A": {"email": "a@example.com"}, "B": {"email": " "}}}`,
			wantErr: "B has no email address",
		},
		{
			name:    "unknown partner",
			config:  `{"participants": {"A": {"email": "a@example.com", "partner": "Nik"}, "B": {"email": "b@example.com"}}}`,
			wantErr: `A's partner "Nik" is not in the config`,
		},
		{
			name:    "one-sided partner",
			config:  `{"participants": {"A": {"email": "a@example.com", "partner": "B"}, "B": {"email": "b@example.com"}}}`,
			wantErr: `A lists B as their partner, but B lists ""`,
		},
		{
			name:    "bad fixed",
			config:  `{"participants": {"A": {"email": "a@example.com", "fixed": "Zed"}, "B": {"email": "b@example.com"}}}`,
			wantErr: "not in the config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadConfig([]byte(tt.config))
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, defaultSubject, cfg.Subject)
			assert.Equal(t, defaultBody, cfg.Body)
		})
	}
}

func TestValidateFixed(t *testing.T) {
	tests := []struct {
		name         string
		participants map[string]Participant
		wantErr      string
	}{
		{
			name: "valid",
			participants: map[string]Participant{
				"Alice": {Fixed: "Bob"},
				"Bob":   {},
				"Carl":  {},
			},
		},
		{
			name: "unknown person",
			participants: map[string]Participant{
				"Alice": {Fixed: "Zed"},
				"Bob":   {},
			},
			wantErr: "not in the config",
		},
		{
			name: "self",
			participants: map[string]Participant{
				"Alice": {Fixed: "alice"},
				"Bob":   {},
			},
			wantErr: "themselves",
		},
		{
			name: "partner",
			participants: map[string]Participant{
				"Alice": {Partner: "Bob", Fixed: "Bob"},
				"Bob":   {Partner: "Alice"},
			},
			wantErr: "partner",
		},
		{
			name: "two givers fixed to same person",
			participants: map[string]Participant{
				"Alice": {Fixed: "Carl"},
				"Bob":   {Fixed: "Carl"},
				"Carl":  {},
			},
			wantErr: "both fixed to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFixed(tt.participants)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePartners(t *testing.T) {
	valid := map[string]Participant{
		"Alice": {Partner: "Bob"},
		"Bob":   {Partner: "alice"},
		"Carl":  {Partner: "NA"},
		"Fay":   {},
	}
	assert.NoError(t, validatePartners(valid))

	err := validatePartners(map[string]Participant{
		"Alice": {Partner: "Bob"},
		"Bob":   {Partner: "Alice"},
		"Dana":  {Partner: "Nik"},
		"Eve":   {Partner: "Alice"},
	})
	// every problem is reported, in name order
	assert.EqualError(t, err, `Dana's partner "Nik" is not in the config
Eve lists Alice as their partner, but Alice lists "Bob"`)
}

func TestMatch(t *testing.T) {
	cfg := &Config{Participants: map[string]Participant{
		"Alice":   {Email: "alice@example.com", Partner: "Bob"},
		"Bob":     {Email: "bob@example.com", Partner: "Alice"},
		"Charlie": {Email: "charlie@example.com", Partner: "Dave"},
		"Dave":    {Email: "dave@example.com", Partner: "Charlie", Fixed: "Alice"},
		"Eve":     {Email: "eve@example.com"},
	}}
	past := map[string]string{"Eve": "Charlie"}

	// the shuffle is random, so repeat to make a lucky pass unlikely
	for range 200 {
		matches, err := match(cfg, past)
		require.NoError(t, err)
		require.Len(t, matches, len(cfg.Participants))

		seen := map[string]bool{}
		for giver, recipient := range matches {
			assert.NotEqual(t, giver, recipient)
			assert.NotEqual(t, cfg.Participants[giver].Partner, recipient)
			assert.False(t, seen[recipient], "%s assigned twice", recipient)
			seen[recipient] = true
		}
		assert.Equal(t, "Alice", matches["Dave"])
		assert.NotEqual(t, "Charlie", matches["Eve"])
	}
}

func TestMatchImpossible(t *testing.T) {
	cfg := &Config{Participants: map[string]Participant{
		"Alice": {Email: "alice@example.com", Partner: "Bob"},
		"Bob":   {Email: "bob@example.com", Partner: "Alice"},
	}}

	_, err := match(cfg, nil)

	assert.ErrorContains(t, err, "no valid matching found")
}

func TestBuildEmails(t *testing.T) {
	cfg, err := loadConfig([]byte(`{
		"budget": "$50",
		"date": "December 24",
		"html": "<p>Hi {{.Giver}}, you have <b>{{.Recipient}}</b></p>",
		"participants": {
			"Alice": {"email": "alice@example.com"},
			"<Bob>": {"email": "bob@example.com"}
		}
	}`))
	require.NoError(t, err)

	emails, err := buildEmails(cfg, map[string]string{"Alice": "<Bob>", "<Bob>": "Alice"})
	require.NoError(t, err)
	require.Len(t, emails, 2)

	byGiver := map[string]email{}
	for _, e := range emails {
		byGiver[e.giver] = e
	}
	alice := byGiver["Alice"]
	assert.Equal(t, "alice@example.com", alice.recipient)
	assert.Equal(t, "<Bob>", alice.assigned)
	assert.Equal(t, defaultSubject, alice.subject)
	assert.Equal(t, `Hello Alice!

Your secret santa recipient is <Bob> 🎁
Budget: $50
Gift exchange: December 24

Merry christmas! 🎄
Santa
`, alice.body)
	assert.Equal(t, "<p>Hi Alice, you have <b>&lt;Bob&gt;</b></p>", alice.html)
}

func TestBuildEmailsDefaultBodyWithoutBudgetOrDate(t *testing.T) {
	cfg, err := loadConfig([]byte(`{"participants": {"A": {"email": "a@example.com"}, "B": {"email": "b@example.com"}}}`))
	require.NoError(t, err)

	emails, err := buildEmails(cfg, map[string]string{"A": "B", "B": "A"})
	require.NoError(t, err)

	for _, e := range emails {
		assert.Equal(t, fmt.Sprintf("Hello %s!\n\nYour secret santa recipient is %s 🎁\n\nMerry christmas! 🎄\nSanta\n", e.giver, e.assigned), e.body)
		assert.Empty(t, e.html)
	}
}

func TestBuildEmailsBadTemplate(t *testing.T) {
	cfg := &Config{Subject: "{{.Nope}}", Body: defaultBody}

	_, err := buildEmails(cfg, map[string]string{"A": "B"})

	assert.ErrorContains(t, err, "rendering subject")
}

func TestReadPastPairings(t *testing.T) {
	setGlobals(t, false)
	cfg := &Config{Participants: map[string]Participant{
		"Nuria": {}, "Bob": {}, "Cy": {},
	}}
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(auditDir, name), []byte(content), 0o600))
	}
	write("Nuria.2025.txt", "giver:     Nuria\nassigned:  Bob\nto:        n@example.com\n")
	// dryruns and other years must be ignored
	write("Bob.dryrun.2025.txt", "giver:     Bob\nassigned:  Cy\n")
	write("Cy.2024.txt", "giver:     Cy\nassigned:  Nuria\n")

	past, err := readPastPairings(cfg, 2025)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Nuria": "Bob"}, past)
}

func TestSendEmail(t *testing.T) {
	setGlobals(t, false)
	ctx := context.Background()
	mockSender := new(MockSESEmailSender)

	e := email{
		giver:     "Nuria",
		assigned:  "Bob",
		recipient: "test@example.com",
		subject:   "Test Subject",
		body:      "Test Body",
		html:      "<p>Test</p>",
	}

	mockSender.On("SendEmail", ctx, mock.AnythingOfType("*sesv2.SendEmailInput"), mock.Anything).
		Return(&sesv2.SendEmailOutput{MessageId: new(string)}, nil)

	sendEmail(ctx, mockSender, e)

	mockSender.AssertNumberOfCalls(t, "SendEmail", 1)
	input := mockSender.Calls[0].Arguments.Get(1).(*sesv2.SendEmailInput)
	assert.Equal(t, "Test Body", *input.Content.Simple.Body.Text.Data)
	assert.Equal(t, "<p>Test</p>", *input.Content.Simple.Body.Html.Data)

	realPath := filepath.Join(auditDir, "Nuria.2026.txt")
	audit, err := os.ReadFile(realPath)
	require.NoError(t, err)
	assert.Equal(t, "giver:     Nuria\nassigned:  Bob\nto:        test@example.com\nfrom:      sender@example.com\nsubject:   Test Subject\nbody:\nTest Body", string(audit))

	// the audit file written this year is what next year's run reads back
	past, err := readPastPairings(&Config{Participants: map[string]Participant{"Nuria": {}}}, 2026)
	require.NoError(t, err)
	assert.Equal(t, "Bob", past["Nuria"])

	// SendEmail should not be called when dryRun is true
	dryRun = true
	sendEmail(ctx, mockSender, e)
	mockSender.AssertNumberOfCalls(t, "SendEmail", 1)

	// dryrun writes its own audit file and leaves the real one untouched
	dryAudit, err := os.ReadFile(filepath.Join(auditDir, "Nuria.dryrun.2026.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(dryAudit), "Test Body")
	unchanged, err := os.ReadFile(realPath)
	require.NoError(t, err)
	assert.Equal(t, audit, unchanged)
}

func TestSendEmailError(t *testing.T) {
	setGlobals(t, false)
	ctx := context.Background()
	mockSender := new(MockSESEmailSender)
	mockSender.On("SendEmail", ctx, mock.Anything, mock.Anything).
		Return((*sesv2.SendEmailOutput)(nil), errors.New("throttled"))

	assert.Panics(t, func() {
		sendEmail(ctx, mockSender, email{giver: "Nuria", recipient: "n@example.com"})
	})

	// a failed send must not leave an audit file claiming it was sent
	_, err := os.Stat(filepath.Join(auditDir, "Nuria.2026.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestNoerr(t *testing.T) {
	assert.NotPanics(t, func() {
		noerr(nil)
	})

	assert.Panics(t, func() {
		noerr(assert.AnError)
	})
}
