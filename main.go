package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

var (
	configFile string
	from       string
	dryRun     bool
	// auditDir is where a copy of each sent email is written, so that matches
	// can be looked up later. It is a var so tests can point it elsewhere.
	auditDir = ".audit"
)

type email struct {
	name      string
	recipient string
	subject   string
	body      string
}

type SESEmailSender interface {
	SendEmail(ctx context.Context, params *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

func main() {
	// parse passed-in flags
	flag.Parse()

	// read the config file
	file, err := os.ReadFile(configFile)
	noerr(err)

	// Create a new AWS config and SES client
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-west-2"))
	noerr(err)
	client := sesv2.NewFromConfig(cfg)

	emails := gatherEmails(file)
	for _, email := range emails {
		sendEmail(ctx, client, email)
	}
}

// anyMatches returns true if any gift givers are found to have been matched to
// themselves or to their partner.
func anyMatches(givers, recipients []string, nameEmailCoupleMap map[string][]string) bool {
	for i := 0; i < len(givers); i++ {
		// did the matching select "themselves"?
		if strings.EqualFold(givers[i], recipients[i]) {
			return true
		}
		// did the matching select the giver's partner?
		partner := nameEmailCoupleMap[givers[i]][1]
		if strings.EqualFold(recipients[i], partner) {
			return true
		}
		// did the matching miss the giver's fixed recipient?
		if fixed := fixedRecipient(nameEmailCoupleMap, givers[i]); fixed != "" && !strings.EqualFold(recipients[i], fixed) {
			return true
		}
	}
	// no matches of givers gifting themselves or partners found, return false.
	return false
}

// fixedRecipient returns the optional third config element: the person the
// giver must be matched to, or "" if there is none.
func fixedRecipient(nameEmailCoupleMap map[string][]string, giver string) string {
	if len(nameEmailCoupleMap[giver]) < 3 {
		return ""
	}
	return nameEmailCoupleMap[giver][2]
}

// validateFixed checks that every fixed match can be satisfied. Without this,
// an impossible rule would make the shuffle loop in gatherEmails run forever.
func validateFixed(nameEmailCoupleMap map[string][]string) error {
	fixedBy := map[string]string{}
	for giver, entry := range nameEmailCoupleMap {
		fixed := fixedRecipient(nameEmailCoupleMap, giver)
		if fixed == "" {
			continue
		}
		found := false
		for name := range nameEmailCoupleMap {
			if strings.EqualFold(name, fixed) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s is fixed to %q, who is not in the config", giver, fixed)
		}
		if strings.EqualFold(giver, fixed) {
			return fmt.Errorf("%s is fixed to themselves", giver)
		}
		if strings.EqualFold(entry[1], fixed) {
			return fmt.Errorf("%s is fixed to their partner %s", giver, fixed)
		}
		key := strings.ToLower(fixed)
		if other, ok := fixedBy[key]; ok {
			return fmt.Errorf("%s and %s are both fixed to %s", other, giver, fixed)
		}
		fixedBy[key] = giver
	}
	return nil
}

// auditPath returns auditDir/<name>.<year>.txt, or auditDir/<name>.dryrun.<year>.txt
// in dryrun mode. Dryruns get their own file so they never overwrite real matches,
// and the year keeps previous years' matches around.
func auditPath(name string) string {
	kind := ""
	if dryRun {
		kind = ".dryrun"
	}
	return filepath.Join(auditDir, fmt.Sprintf("%s%s.%d.txt", name, kind, time.Now().Year()))
}

// writeAudit saves a copy of the email to the giver's audit file.
func writeAudit(e email) {
	noerr(os.MkdirAll(auditDir, 0o700))
	content := fmt.Sprintf("recipient: %s\nfrom:      %s\nsubject:   %s\nbody:\n%s", e.recipient, from, e.subject, e.body)
	noerr(os.WriteFile(auditPath(e.name), []byte(content), 0o600))
}

// sendEmail sends an email to the given recipient (email address), with the given
// subject and message body.
func sendEmail(ctx context.Context, client SESEmailSender, e email) {
	if dryRun {
		fmt.Printf("recipient: %s\n", e.recipient)
		fmt.Printf("from:      %s\n", from)
		fmt.Printf("subject:   %s\n", e.subject)
		fmt.Printf("body:\n%s------------------------------------------------\n\n", e.body)
		writeAudit(e)
		return
	}

	// Assemble the email.
	input := &sesv2.SendEmailInput{
		Destination: &types.Destination{
			ToAddresses: []string{e.recipient},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Body: &types.Body{
					Text: &types.Content{
						Data: aws.String(e.body),
					},
				},
				Subject: &types.Content{
					Data: aws.String(e.subject),
				},
			},
		},
		FromEmailAddress: aws.String(from),
	}

	out, err := client.SendEmail(ctx, input)
	noerr(err)
	fmt.Printf("Sent email to %s, message ID: %s\n", e.recipient, *out.MessageId)
	writeAudit(e)
	// by default SES limits users to one email per second, so sleep here to avoid hitting the limit
	time.Sleep(500 * time.Millisecond)
}

func gatherEmails(configFile []byte) []email {
	// mapping of names of participants to their email and 'couple'
	nameEmailCoupleMap := map[string][]string{}
	err := json.Unmarshal(configFile, &nameEmailCoupleMap)
	noerr(err)
	noerr(validateFixed(nameEmailCoupleMap))
	giftGivers := []string{}
	giftRecipients := []string{}
	for k := range nameEmailCoupleMap {
		giftGivers = append(giftGivers, k)
		giftRecipients = append(giftRecipients, k)
	}

	for true {
		// keep shuffling the recipients list until none of the 'recipients' match
		// the 'givers'
		rand.Shuffle(len(giftRecipients), func(i, j int) {
			giftRecipients[i], giftRecipients[j] = giftRecipients[j], giftRecipients[i]
		})
		if anyMatches(giftGivers, giftRecipients, nameEmailCoupleMap) {
			continue
		}
		break
	}

	emails := []email{}
	for i := 0; i < len(giftGivers); i++ {
		body := fmt.Sprintf(`Hello %s!

Your secret santa recipient is %s 🎁

Merry christmas! 🎄
Santa
`, giftGivers[i], giftRecipients[i])
		subject := "Secret Santa 🎅"
		giverEmail := nameEmailCoupleMap[giftGivers[i]][0]
		emails = append(emails, email{
			name:      giftGivers[i],
			recipient: giverEmail,
			subject:   subject,
			body:      body,
		})
	}
	return emails
}

func init() {
	flag.StringVar(&configFile, "file", "config.test.json", "Config file, mapping names of participants to their email addresses.")
	flag.StringVar(&from, "from", "", "Email address to send the email from.")
	flag.BoolVar(&dryRun, "dryrun", false, "Specify this flag to only print the emails, no emails will be sent.")
}

func noerr(err error) {
	if err != nil {
		panic(err)
	}
}
