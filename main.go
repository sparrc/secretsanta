package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

const (
	defaultSubject = "Secret Santa 🎅"
	defaultBody    = `Hello {{.Giver}}!

Your secret santa recipient is {{.Recipient}} 🎁
{{if .Budget}}Budget: {{.Budget}}
{{end}}{{if .Date}}Gift exchange: {{.Date}}
{{end}}
Merry christmas! 🎄
Santa
`
	// maxShuffles bounds the search for a valid matching, so configs whose
	// rules can't all be met return an error instead of looping forever.
	maxShuffles = 1_000_000
	// noPartner is a partner value that means "no partner" without
	// failing the unknown-partner check.
	noPartner = "NA"
)

var (
	configFile string
	from       string
	dryRun     bool
	// auditDir is where a copy of each sent email is written, so that matches
	// can be looked up later. It is a var so tests can point it elsewhere.
	auditDir = ".audit"
	// sendDelay is the pause between sends. It is a var so tests can skip it.
	sendDelay = 500 * time.Millisecond
	// now is a var so tests can control the year used for audit files.
	now = time.Now
)

// Config is the JSON config file.
type Config struct {
	// Subject, Body and HTML are Go templates. Body defaults to a plain
	// greeting; HTML is optional and, when set, is sent alongside Body.
	Subject      string                 `json:"subject"`
	Body         string                 `json:"body"`
	HTML         string                 `json:"html"`
	Budget       string                 `json:"budget"`
	Date         string                 `json:"date"`
	Participants map[string]Participant `json:"participants"`
}

// Participant is one person in the gift exchange.
type Participant struct {
	Email string `json:"email"`
	// Partner is never matched with this person. Empty or "NA" means none.
	Partner string `json:"partner"`
	// Fixed, when set, is always matched as this person's recipient.
	Fixed string `json:"fixed"`
}

// templateData is the data available to the subject, body and HTML templates.
type templateData struct {
	Giver     string
	Recipient string
	Budget    string
	Date      string
}

type email struct {
	giver     string
	assigned  string
	recipient string
	subject   string
	body      string
	html      string
}

type SESEmailSender interface {
	SendEmail(ctx context.Context, params *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

func main() {
	flag.Parse()
	if configFile == "" || from == "" {
		fmt.Fprintln(os.Stderr, "-file and -from are required")
		flag.Usage()
		os.Exit(2)
	}

	file, err := os.ReadFile(configFile)
	noerr(err)
	cfg, err := loadConfig(file)
	noerr(err)

	past, err := readPastPairings(cfg, now().Year()-1)
	noerr(err)
	matches, err := match(cfg, past)
	noerr(err)
	emails, err := buildEmails(cfg, matches)
	noerr(err)

	// Create a new AWS config and SES client
	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-west-2"))
	noerr(err)
	client := sesv2.NewFromConfig(awsCfg)

	for _, e := range emails {
		sendEmail(ctx, client, e)
	}
}

// loadConfig parses and validates the config file, filling in default
// templates.
func loadConfig(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	// catch misspelled keys like "parnter", which would otherwise be ignored
	dec.DisallowUnknownFields()
	cfg := &Config{}
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Subject == "" {
		cfg.Subject = defaultSubject
	}
	if cfg.Body == "" {
		cfg.Body = defaultBody
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validateConfig returns an error for configs that can't produce a valid set
// of emails.
func validateConfig(cfg *Config) error {
	if len(cfg.Participants) < 2 {
		return fmt.Errorf("config needs at least 2 participants, found %d", len(cfg.Participants))
	}
	for name, p := range cfg.Participants {
		if strings.TrimSpace(p.Email) == "" {
			return fmt.Errorf("%s has no email address", name)
		}
	}
	if err := validatePartners(cfg.Participants); err != nil {
		return err
	}
	return validateFixed(cfg.Participants)
}

// findName returns the config key matching name case-insensitively.
func findName(participants map[string]Participant, name string) (string, bool) {
	for k := range participants {
		if strings.EqualFold(k, name) {
			return k, true
		}
	}
	return "", false
}

// validateFixed checks the obvious ways a fixed match can be impossible, so the
// user gets a specific error rather than the generic one from match.
func validateFixed(participants map[string]Participant) error {
	fixedBy := map[string]string{}
	for giver, p := range participants {
		if p.Fixed == "" {
			continue
		}
		if _, ok := findName(participants, p.Fixed); !ok {
			return fmt.Errorf("%s is fixed to %q, who is not in the config", giver, p.Fixed)
		}
		if strings.EqualFold(giver, p.Fixed) {
			return fmt.Errorf("%s is fixed to themselves", giver)
		}
		if strings.EqualFold(p.Partner, p.Fixed) {
			return fmt.Errorf("%s is fixed to their partner %s", giver, p.Fixed)
		}
		key := strings.ToLower(p.Fixed)
		if other, ok := fixedBy[key]; ok {
			return fmt.Errorf("%s and %s are both fixed to %s", other, giver, p.Fixed)
		}
		fixedBy[key] = giver
	}
	return nil
}

// validatePartners rejects partner values that look like typos: names not in
// the config, and pairs where the partner lists someone else. It reports every
// problem at once so they can all be fixed in one pass.
func validatePartners(participants map[string]Participant) error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(participants)) {
		p := participants[name]
		if p.Partner == "" || strings.EqualFold(p.Partner, noPartner) {
			continue
		}
		partnerKey, ok := findName(participants, p.Partner)
		if !ok {
			errs = append(errs, fmt.Errorf("%s's partner %q is not in the config", name, p.Partner))
			continue
		}
		if back := participants[partnerKey].Partner; !strings.EqualFold(back, name) {
			errs = append(errs, fmt.Errorf("%s lists %s as their partner, but %s lists %q", name, partnerKey, partnerKey, back))
		}
	}
	return errors.Join(errs...)
}

// breaksRules returns true if any giver is matched to themselves, to their
// partner, to someone other than their fixed recipient, or (when they have no
// fixed recipient) to the person they had last year.
func breaksRules(givers, recipients []string, participants map[string]Participant, past map[string]string) bool {
	for i, giver := range givers {
		p := participants[giver]
		if strings.EqualFold(giver, recipients[i]) {
			return true
		}
		if strings.EqualFold(recipients[i], p.Partner) {
			return true
		}
		if p.Fixed != "" {
			// a fixed match is an explicit choice, so it wins over last year's pairing
			if !strings.EqualFold(recipients[i], p.Fixed) {
				return true
			}
			continue
		}
		if last := past[giver]; last != "" && strings.EqualFold(recipients[i], last) {
			return true
		}
	}
	return false
}

// match returns a map of giver name to recipient name that follows all rules.
// past maps giver name to last year's recipient.
func match(cfg *Config, past map[string]string) (map[string]string, error) {
	var givers []string
	for name := range cfg.Participants {
		givers = append(givers, name)
	}
	recipients := append([]string(nil), givers...)

	for range maxShuffles {
		rand.Shuffle(len(recipients), func(i, j int) {
			recipients[i], recipients[j] = recipients[j], recipients[i]
		})
		if breaksRules(givers, recipients, cfg.Participants, past) {
			continue
		}
		matches := map[string]string{}
		for i, giver := range givers {
			matches[giver] = recipients[i]
		}
		return matches, nil
	}
	return nil, fmt.Errorf("no valid matching found after %d shuffles: the partner, fixed and last-year rules can't all be met", maxShuffles)
}

// buildEmails renders one email per giver from the config templates.
func buildEmails(cfg *Config, matches map[string]string) ([]email, error) {
	subjectTmpl, err := template.New("subject").Parse(cfg.Subject)
	if err != nil {
		return nil, fmt.Errorf("parsing subject template: %w", err)
	}
	bodyTmpl, err := template.New("body").Parse(cfg.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing body template: %w", err)
	}
	var htmlTmpl *htmltemplate.Template
	if cfg.HTML != "" {
		// html/template escapes names, so a "<" in a name can't break the markup
		if htmlTmpl, err = htmltemplate.New("html").Parse(cfg.HTML); err != nil {
			return nil, fmt.Errorf("parsing html template: %w", err)
		}
	}

	var emails []email
	for giver, recipient := range matches {
		data := templateData{Giver: giver, Recipient: recipient, Budget: cfg.Budget, Date: cfg.Date}
		var subject, body, html strings.Builder
		if err := subjectTmpl.Execute(&subject, data); err != nil {
			return nil, fmt.Errorf("rendering subject: %w", err)
		}
		if err := bodyTmpl.Execute(&body, data); err != nil {
			return nil, fmt.Errorf("rendering body: %w", err)
		}
		if htmlTmpl != nil {
			if err := htmlTmpl.Execute(&html, data); err != nil {
				return nil, fmt.Errorf("rendering html: %w", err)
			}
		}
		emails = append(emails, email{
			giver:     giver,
			assigned:  recipient,
			recipient: cfg.Participants[giver].Email,
			subject:   subject.String(),
			body:      body.String(),
			html:      html.String(),
		})
	}
	return emails, nil
}

// auditPath returns auditDir/<name>.<year>.txt, or auditDir/<name>.dryrun.<year>.txt
// when dryrun is set. Dryruns get their own file so they never overwrite real
// matches, and the year keeps previous years' matches around.
func auditPath(name string, year int, dryrun bool) string {
	kind := ""
	if dryrun {
		kind = ".dryrun"
	}
	return filepath.Join(auditDir, fmt.Sprintf("%s%s.%d.txt", name, kind, year))
}

// writeAudit saves a copy of the email to the giver's audit file. The
// "assigned:" line is what readPastPairings reads back next year.
func writeAudit(e email) {
	noerr(os.MkdirAll(auditDir, 0o700))
	content := fmt.Sprintf("giver:     %s\nassigned:  %s\nto:        %s\nfrom:      %s\nsubject:   %s\nbody:\n%s",
		e.giver, e.assigned, e.recipient, from, e.subject, e.body)
	noerr(os.WriteFile(auditPath(e.giver, now().Year(), dryRun), []byte(content), 0o600))
}

// readPastPairings reads each participant's real (non-dryrun) audit file for
// the given year and returns a map of giver name to who they were assigned.
// Missing files are skipped.
func readPastPairings(cfg *Config, year int) (map[string]string, error) {
	past := map[string]string{}
	for name := range cfg.Participants {
		data, err := os.ReadFile(auditPath(name, year, false))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for line := range strings.Lines(string(data)) {
			if assigned, ok := strings.CutPrefix(line, "assigned:"); ok {
				past[name] = strings.TrimSpace(assigned)
				break
			}
		}
	}
	return past, nil
}

// sendEmail sends an email to the given recipient (email address), with the given
// subject and message body.
func sendEmail(ctx context.Context, client SESEmailSender, e email) {
	if dryRun {
		fmt.Printf("recipient: %s\n", e.recipient)
		fmt.Printf("from:      %s\n", from)
		fmt.Printf("subject:   %s\n", e.subject)
		fmt.Printf("body:\n%s", e.body)
		if e.html != "" {
			fmt.Printf("html:\n%s\n", e.html)
		}
		fmt.Printf("------------------------------------------------\n\n")
		writeAudit(e)
		return
	}

	body := &types.Body{
		Text: &types.Content{Data: aws.String(e.body)},
	}
	if e.html != "" {
		body.Html = &types.Content{Data: aws.String(e.html)}
	}
	input := &sesv2.SendEmailInput{
		Destination: &types.Destination{
			ToAddresses: []string{e.recipient},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Body:    body,
				Subject: &types.Content{Data: aws.String(e.subject)},
			},
		},
		FromEmailAddress: aws.String(from),
	}

	out, err := client.SendEmail(ctx, input)
	noerr(err)
	fmt.Printf("Sent email to %s, message ID: %s\n", e.recipient, *out.MessageId)
	writeAudit(e)
	// SES caps each account's send rate (1/sec in the sandbox, 14/sec or more
	// once out of it). Pausing 500ms keeps a production account well under its cap.
	time.Sleep(sendDelay)
}

func init() {
	flag.StringVar(&configFile, "file", "", "Config file listing participants and email settings (required).")
	flag.StringVar(&from, "from", "", "Email address to send the email from (required).")
	flag.BoolVar(&dryRun, "dryrun", false, "Specify this flag to only print the emails, no emails will be sent.")
}

func noerr(err error) {
	if err != nil {
		panic(err)
	}
}
