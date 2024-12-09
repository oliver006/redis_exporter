<!-- Text inside of HTML comment blocks will NOT appear in your pull request description -->
<!-- Formatting information can be found at https://www.markdownguide.org/basic-syntax/ -->
# Overview

SNOW-XXXXX

<!--
Why is this review being requested?  The full details should be in the JIRA, but the review should focus on the fix/change being implemented.
If there are multiple steps in the Jira, which step is this?
-->

## Pre-review checklist
<!-- - [ ] This change fixes Jira SNOW-XXXXX -->
- [ ] I attest that this change meets the bar for low risk without security requirements as defined in the [Accelerated Risk Assessment Criteria](https://developer-handbook.m1.us-west-2.aws.app.snowflake.com/docs/reference/security-review/accelerated-risk-assessment/#eligibility) and I have taken the [Risk Assessment Training in Workday](https://wd5.myworkday.com/snowflake/learning/course/6c613806284a1001f111fedf3e4e0000).
    - Checking this checkbox is mandatory if using the [Accelerated Risk Assessment](https://developer-handbook.m1.us-west-2.aws.app.snowflake.com/docs/reference/security-review/accelerated-risk-assessment/) to risk assess the changes in this Pull Request.
    - If this change does not meet the bar for low risk without security requirements (as confirmed by the peer reviewers of this pull request) then a [formal Risk Assessment](https://developer-handbook.m1.us-west-2.aws.app.snowflake.com/docs/reference/security-review/risk-assessment/) must be completed. Please note that a formal Risk Assessment will require you to spend extra time performing a security review for this change. Please account for this extra time earlier rather than later to avoid unnecessary delays in the release process.
- [ ] This change has passed precommit
- [ ] This change is TEST-ONLY
- [ ] This change has been through a design review process and the test plan is signed off by the owner
- [ ] This change is parameter protected
- [ ] This change has code coverage for the new code added
<!-- - [ ] This change requires security review -->
<!-- - [ ] This change includes parameter changes -->
