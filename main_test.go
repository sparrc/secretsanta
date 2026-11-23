package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockSESEmailSender is a mock implementation of SESEmailSender interface
type MockSESEmailSender struct {
	mock.Mock
}

func (m *MockSESEmailSender) SendEmail(ctx context.Context, params *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	args := m.Called(ctx, params, optFns)
	return args.Get(0).(*sesv2.SendEmailOutput), args.Error(1)
}

func TestAnyMatches(t *testing.T) {
	tests := []struct {
		name               string
		givers             []string
		recipients         []string
		nameEmailCoupleMap map[string][]string
		expected           bool
	}{
		{
			name:       "No matches",
			givers:     []string{"Alice", "Bob", "Charlie"},
			recipients: []string{"Charlie", "Alice", "Bob"},
			nameEmailCoupleMap: map[string][]string{
				"Alice":   {"alice@example.com", "Dave"},
				"Bob":     {"bob@example.com", "Eve"},
				"Charlie": {"charlie@example.com", "Frank"},
			},
			expected: false,
		},
		{
			name:       "Match with self",
			givers:     []string{"Alice", "Bob", "Charlie"},
			recipients: []string{"Alice", "Charlie", "Bob"},
			nameEmailCoupleMap: map[string][]string{
				"Alice":   {"alice@example.com", "Dave"},
				"Bob":     {"bob@example.com", "Eve"},
				"Charlie": {"charlie@example.com", "Frank"},
			},
			expected: true,
		},
		{
			name:       "Match with partner",
			givers:     []string{"Alice", "Bob", "Charlie"},
			recipients: []string{"Charlie", "Eve", "Bob"},
			nameEmailCoupleMap: map[string][]string{
				"Alice":   {"alice@example.com", "Dave"},
				"Bob":     {"bob@example.com", "Eve"},
				"Charlie": {"charlie@example.com", "Frank"},
			},
			expected: true,
		},
		{
			name:       "Fixed recipient satisfied",
			givers:     []string{"Alice", "Bob", "Charlie"},
			recipients: []string{"Charlie", "Alice", "Bob"},
			nameEmailCoupleMap: map[string][]string{
				"Alice":   {"alice@example.com", "", "charlie"},
				"Bob":     {"bob@example.com", ""},
				"Charlie": {"charlie@example.com", "", ""},
			},
			expected: false,
		},
		{
			name:       "Fixed recipient missed",
			givers:     []string{"Alice", "Bob", "Charlie"},
			recipients: []string{"Bob", "Charlie", "Alice"},
			nameEmailCoupleMap: map[string][]string{
				"Alice":   {"alice@example.com", "", "Charlie"},
				"Bob":     {"bob@example.com", ""},
				"Charlie": {"charlie@example.com", ""},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := anyMatches(tt.givers, tt.recipients, tt.nameEmailCoupleMap)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGatherEmails(t *testing.T) {
	configFile := []byte(`{
		"Alice": ["alice@example.com", "Bob"],
		"Bob": ["bob@example.com", "Alice"],
		"Charlie": ["charlie@example.com", "Dave"],
		"Dave": ["dave@example.com", "Charlie"]
	}`)

	emails := gatherEmails(configFile)

	assert.Len(t, emails, 4)

	// Check that all emails are unique
	recipients := make(map[string]bool)
	for _, email := range emails {
		assert.False(t, recipients[email.recipient], "Duplicate recipient found")
		recipients[email.recipient] = true

		assert.Equal(t, "Secret Santa 🎅", email.subject)
		assert.Contains(t, email.body, "Your secret santa recipient is")
	}
}

func TestGatherEmailsFixed(t *testing.T) {
	configFile := []byte(`{
		"Alice": ["alice@example.com", "Bob", "Charlie"],
		"Bob": ["bob@example.com", "Alice", "Dave"],
		"Charlie": ["charlie@example.com", "Dave"],
		"Dave": ["dave@example.com", "Charlie"]
	}`)

	// the shuffle is random, so repeat to make a lucky pass unlikely
	for i := 0; i < 50; i++ {
		for _, e := range gatherEmails(configFile) {
			switch e.name {
			case "Alice":
				assert.Contains(t, e.body, "recipient is Charlie")
			case "Bob":
				assert.Contains(t, e.body, "recipient is Dave")
			}
		}
	}
}

func TestValidateFixed(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string][]string
		wantErr string
	}{
		{
			name: "valid",
			config: map[string][]string{
				"Alice": {"a@example.com", "", "Bob"},
				"Bob":   {"b@example.com", ""},
				"Carl":  {"c@example.com", "", ""},
			},
		},
		{
			name: "unknown person",
			config: map[string][]string{
				"Alice": {"a@example.com", "", "Zed"},
				"Bob":   {"b@example.com", ""},
			},
			wantErr: "not in the config",
		},
		{
			name: "self",
			config: map[string][]string{
				"Alice": {"a@example.com", "", "alice"},
				"Bob":   {"b@example.com", ""},
			},
			wantErr: "themselves",
		},
		{
			name: "partner",
			config: map[string][]string{
				"Alice": {"a@example.com", "Bob", "Bob"},
				"Bob":   {"b@example.com", "Alice"},
			},
			wantErr: "partner",
		},
		{
			name: "two givers fixed to same person",
			config: map[string][]string{
				"Alice": {"a@example.com", "", "Carl"},
				"Bob":   {"b@example.com", "", "Carl"},
				"Carl":  {"c@example.com", ""},
			},
			wantErr: "both fixed to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFixed(tt.config)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestSendEmail(t *testing.T) {
	ctx := context.Background()
	mockSender := new(MockSESEmailSender)
	auditDir = filepath.Join(t.TempDir(), ".audit")

	e := email{
		name:      "Nuria",
		recipient: "test@example.com",
		subject:   "Test Subject",
		body:      "Test Body",
	}

	mockSender.On("SendEmail", ctx, mock.AnythingOfType("*sesv2.SendEmailInput"), mock.Anything).
		Return(&sesv2.SendEmailOutput{MessageId: new(string)}, nil)

	// Test with dryRun = false
	dryRun = false
	from = "sender@example.com"
	sendEmail(ctx, mockSender, e)

	mockSender.AssertCalled(t, "SendEmail", ctx, mock.AnythingOfType("*sesv2.SendEmailInput"), mock.Anything)

	year := time.Now().Year()
	audit, err := os.ReadFile(filepath.Join(auditDir, fmt.Sprintf("Nuria.%d.txt", year)))
	assert.NoError(t, err)
	assert.Contains(t, string(audit), "test@example.com")
	assert.Contains(t, string(audit), "Test Body")

	// Test with dryRun = true
	dryRun = true
	sendEmail(ctx, mockSender, e)

	// SendEmail should not be called when dryRun is true
	mockSender.AssertNumberOfCalls(t, "SendEmail", 1)

	// dryrun writes its own audit file and leaves the real one untouched
	dryAudit, err := os.ReadFile(filepath.Join(auditDir, fmt.Sprintf("Nuria.dryrun.%d.txt", year)))
	assert.NoError(t, err)
	assert.Contains(t, string(dryAudit), "Test Body")
	_, err = os.Stat(filepath.Join(auditDir, fmt.Sprintf("Nuria.%d.txt", year)))
	assert.NoError(t, err)
}

func TestNoerr(t *testing.T) {
	assert.NotPanics(t, func() {
		noerr(nil)
	})

	assert.Panics(t, func() {
		noerr(assert.AnError)
	})
}
