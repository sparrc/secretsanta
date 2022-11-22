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

	nameMap := map[string]string{}
	err = json.Unmarshal(file, &nameMap)
	noerr(err)
	names := []string{}
	names2 := []string{}
	for k := range nameMap {
		names = append(names, k)
		names2 = append(names2, k)
	}

	for true {
		rand.Shuffle(len(names2), func(i, j int) {
			names2[i], names2[j] = names2[j], names2[i]
		})
		if anyMatches(names, names2) {
			continue
		}
		break
	}

	for i := 0; i < len(names); i++ {
		body := fmt.Sprintf(`Hello %s!

Your secret santa recipient is %s 🎁

Merry christmas! 🎄
Santa
`, names[i], names2[i])
		subject := "Secret Santa 🎅"
		sendEmail(nameMap[names[i]], subject, body)
	}
}

func init() {
	flag.StringVar(&configFile, "file", "config.test.json", "Config file, mapping names of participants to their email addresses.")
	flag.StringVar(&from, "from", "", "Email address to send the email from.")
	flag.BoolVar(&dryRun, "dryrun", false, "Specify this flag to only print the emails, no emails will be sent.")
}
