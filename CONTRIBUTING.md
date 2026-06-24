# Contribution guidelines

👋 Howdy!

Thanks for your interest in `go-httpbin`! Here are a few quick notes about
contributing to this project. If anything is unclear or you have questions
not answered here, please feel free to open an issue.

## Expectations of contributors

### Please be patient

This project is maintained by one person with limited and _extremely variable_
time and bandwidth to respond to issues and review pull requests.

> [!IMPORTANT]
> I'm sorry in advance, but do not count on timely feedback!

### Please be a human

This project does not take any hard-line stances against using LLMs to write
code, but contributions should come from humans who have put some thought and
effort into their work.

Purely automated contributions will be rejected (see, e.g. [#264][]).

### Please open an issue before opening a pull request

To minimize wasted effort on everyone's part, please open an issue _before_
opening a pull request, unless a fix is glaringly obvious.

- **Find a bug?** Open an issue to verify intended behavior and make sure
  we're aligned on a fix.

- **Have a feature request?** Open an issue to make sure it makes sense for
  the project and, yep, make sure we're aligned on an implementation.

- **Find a security issue?** <del>Open an issue</del> See [SECURITY.md][].

## HOWTO contribute

### Issues

Before writing an issue, search for existing reports/suggestions.

When reporting a bug, please include, at a minimum:
- expected behavior
- observed behavior
- steps to reproduce

When submitting a feature request, please include concrete use cases,
motivations, inducements, etc.

### Pull requests

Before opening a pull request:

- **Add tests that cover your changes.** Please help maintain a high bar for
  code coverage by adding tests for any code changes you make.

  (The test suite can be a bit hairy and testing some edge case behavior might
  not be worth the effort/complexity, so feel free to ask for guidance.)

- **Format/lint/test.** If you're contributing code, make sure
  tests pass, no linting issues are found, and the code is correctly
  formatted:

  ```bash
  make fmt lint test

  # or, if you're not into the whole brevity thing
  make fmt
  make lint
  make test
  ```

After opening a pull request:

- **Expect an iterative code review process.** Be prepared to receive and
  respond to feedback on your code!

- **Address automated test failures.** A variety of automated tests are run
  on every pull request, please address any failures.

Your change will be incorporated into the next [release][] after it is merged,
but this project makes no guarantees about release cadence or timing.

[SECURITY.md]: ./SECURITY.md
[#264]: https://github.com/mccutchen/go-httpbin/pull/264
[release]: https://github.com/mccutchen/go-httpbin/releases
