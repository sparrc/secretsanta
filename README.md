# Secret Santa 🎅

Generate a secret santa matchup and email the participants!

# Instructions

1. Setup SES and remove the sandbox on your AWS account: https://docs.aws.amazon.com/ses/latest/DeveloperGuide/request-production-access.html.
2. You must setup a sender domain/email that you can use via AWS SES.
3. Setup AWS credential access.
4. Create a config file (recommended that you try with some test email addresses first):
```
cat >./config.json <<'EOF'
{
    "budget": "$50",
    "date": "December 24",
    "participants": {
        "Peppa Pig":  {"email": "peppapig@hotmail.com", "fixed": "George"},
        "George":     {"email": "georgepig@gmail.com"},
        "Daddy Pig":  {"email": "daddypig@gmail.com", "partner": "Mummy Pig"},
        "Mummy Pig":  {"email": "mummypig@yahoo.com", "partner": "Daddy Pig"},
        "Suzy Sheep": {"email": "suzy@hotmail.com"}
    }
}
EOF
```
Each participant has:
- `email` (required): the address their email is sent to.
- `partner` (optional): the person's couple. Couples are never matched together. Use `"NA"` or leave it out for no partner. The program exits with an error, in dryrun too, if a partner isn't in the config or if two people don't list each other.
- `fixed` (optional): always assign this person as their recipient.

Optional email settings:
- `budget` and `date`: shown in the default email body when set.
- `subject` and `body`: [Go templates](https://pkg.go.dev/text/template) that replace the default subject and body. They can use `{{.Giver}}`, `{{.Recipient}}`, `{{.Budget}}` and `{{.Date}}`.
- `html`: an HTML version of the body, using the same fields. When set, it is sent alongside the plain text body.

Nobody is given the same person they had last year, unless it is their `fixed` match. Last year's matches are read from the `.audit/` files (see below).

5. Run a dryrun of the program (no emails are sent):
```
go run main.go -file config.json -from secretsanta@mydomain.com -dryrun
```
6. Run the program (🚨 emails will be sent! 🚨):
```
go run main.go -file config.json -from secretsanta@mydomain.com
```

`-file` and `-from` are always required.

A copy of each sent email is written to `.audit/<name>.<year>.txt` (for example `.audit/Nuria.2026.txt`), so you can check who somebody has later. Dryruns write to `.audit/<name>.dryrun.<year>.txt` instead, so they never overwrite the real matches.
