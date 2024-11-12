package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
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
)

type email struct {
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
	}
	// no matches of givers gifting themselves or partners found, return false.
	return false
}

// sendEmail sends an email to the given recipient (email address), with the given
// subject and message body.
func sendEmail(ctx context.Context, client SESEmailSender, e email) {
	if dryRun {
		fmt.Printf("recipient: %s\n", e.recipient)
		fmt.Printf("from:      %s\n", from)
		fmt.Printf("subject:   %s\n", e.subject)
		fmt.Printf("body:\n%s------------------------------------------------\n\n", e.body)
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
	// by default SES limits users to one email per second, so sleep here to avoid hitting the limit
	time.Sleep(500 * time.Millisecond)
}

func gatherEmails(configFile []byte) []email {
	// mapping of names of participants to their email and 'couple'
	nameEmailCoupleMap := map[string][]string{}
	err := json.Unmarshal(configFile, &nameEmailCoupleMap)
	noerr(err)
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
