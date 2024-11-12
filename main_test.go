package main

import (
	"context"
	"testing"

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

func TestSendEmail(t *testing.T) {
	ctx := context.Background()
	mockSender := new(MockSESEmailSender)

	e := email{
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

	// Test with dryRun = true
	dryRun = true
	sendEmail(ctx, mockSender, e)

	// SendEmail should not be called when dryRun is true
	mockSender.AssertNumberOfCalls(t, "SendEmail", 1)
}

func TestNoerr(t *testing.T) {
	assert.NotPanics(t, func() {
		noerr(nil)
	})

	assert.Panics(t, func() {
		noerr(assert.AnError)
	})
}
