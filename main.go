package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sesv2"
)

var (
	configFile string
	from       string
	dryRun     bool
)

func noerr(err error) {
	if err != nil {
		panic(err)
	}
}

func anyMatches(a1, a2 []string) bool {
	for i := 0; i < len(a1); i++ {
		if strings.EqualFold(a1[i], a2[i]) {
			return true
		}
	}
	return false
}

func sendEmail(recipient, subject, body string) {
	if dryRun {
		fmt.Printf("recipient: %s\n", recipient)
		fmt.Printf("from:      %s\n", from)
		fmt.Printf("subject:   %s\n", subject)
		fmt.Printf("body:\n%s------------------------------------------------\n\n", body)
		return
	}
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String("us-west-2")},
	)
	noerr(err)

	// Create an SES session.
	svc := sesv2.New(sess)

	// Assemble the email.
	input := &sesv2.SendEmailInput{
		Destination: &sesv2.Destination{
			ToAddresses: []*string{
				aws.String(recipient),
			},
		},
		Content: &sesv2.EmailContent{
			Simple: &sesv2.Message{
				Body: &sesv2.Body{
					Text: &sesv2.Content{
						Data: aws.String(body),
					},
				},
				Subject: &sesv2.Content{
					Data: aws.String(subject),
				},
			},
		},
		FromEmailAddress: aws.String(from),
	}

	out, err := svc.SendEmail(input)
	noerr(err)
	fmt.Printf("Sent email to %s, message ID: %s\n", recipient, *out.MessageId)
	// by default SES limits users to one email per second, so sleep here to avoid hitting the limit
	time.Sleep(500 * time.Millisecond)
}

func main() {
	flag.Parse()
	rand.Seed(time.Now().UnixNano())
	file, err := os.ReadFile(configFile)
	noerr(err)

	// mapping of names of participants to their email
	nameEmailMap := map[string]string{}
	err = json.Unmarshal(file, &nameEmailMap)
	noerr(err)
	giftGivers := []string{}
	giftRecipients := []string{}
	for k := range nameEmailMap {
		giftGivers = append(giftGivers, k)
		giftRecipients = append(giftRecipients, k)
	}

	for true {
		// keep shuffling the recipients list until none of the 'recipients' match
		// the 'givers'
		rand.Shuffle(len(giftRecipients), func(i, j int) {
			giftRecipients[i], giftRecipients[j] = giftRecipients[j], giftRecipients[i]
		})
		if anyMatches(giftGivers, giftRecipients) {
			continue
		}
		break
	}

	for i := 0; i < len(giftGivers); i++ {
		body := fmt.Sprintf(`Hello %s!

Your secret santa recipient is %s 🎁

Merry christmas! 🎄
Santa
`, giftGivers[i], giftRecipients[i])
		subject := "Secret Santa 🎅"
		sendEmail(nameEmailMap[giftGivers[i]], subject, body)
	}
}

func init() {
	flag.StringVar(&configFile, "file", "config.test.json", "Config file, mapping names of participants to their email addresses.")
	flag.StringVar(&from, "from", "", "Email address to send the email from.")
	flag.BoolVar(&dryRun, "dryrun", false, "Specify this flag to only print the emails, no emails will be sent.")
}
