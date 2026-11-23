# Secret Santa 🎅

Generate a secret santa matchup and email the participants!

# Instructions

1. Setup SES and remove the sandbox on your AWS account: https://docs.aws.amazon.com/ses/latest/DeveloperGuide/request-production-access.html.
2. You must setup a sender domain/email that you can use via AWS SES.
3. Setup AWS credential access.
4. Create a config file (recommended that you try with some test email addresses first):
```
# first element of the list is the email address of the person.
# second element of the list is the person's "couple" if they have one, this ensures
#   that couples are not matched together.
# third element (optional) FIXES the person's match: they will always be
#   assigned the named person. Leave it out or set it to "" for a random match.
cat >./config.json <<EOF
{
    "Peppa Pig":  ["peppapig@hotmail.com", "", "George"],
    "George":     ["georgepig@gmail.com", ""],
    "Daddy Pig":  ["daddypig@gmail.com", "Mummy Pig"],
    "Mummy Pig":  ["mummypig@yahoo.com", "Daddy Pig"],
    "Suzy Sheep": ["suzy@hotmail.com", ""]
}
EOF
```
5. Run a dryrun of the program (no emails sent or SES client created):
```
go run main.go -file config.json -from secretsanta@mydomain.com -dryrun
```
6. Run the program (🚨 emails will be sent! 🚨):
```
go run main.go -file config.json -from secretsanta@mydomain.com
```

A copy of each sent email is written to `.audit/<name>.<year>.txt` (for example `.audit/Nuria.2026.txt`), so you can check who somebody has later. Dryruns write to `.audit/<name>.dryrun.<year>.txt` instead, so they never overwrite the real matches.
