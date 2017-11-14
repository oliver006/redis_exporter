package cfenv_test

import (
	. "github.com/cloudfoundry-community/go-cfenv"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Environment", func() {
	Describe("Environment variables should be mapped", func() {
		Context("With default environment", func() {
			It("Should contain at least one mapped variable", func() {
				vars := CurrentEnv()
				Ω(len(vars)).Should(BeNumerically(">", 0), "Environment variables should exist")
			})

			It("Should split variables into keys and values", func() {
				vars := CurrentEnv()
				valueCount := 0
				for k, v := range vars {
					// Key should never be empty
					Ω(k).ShouldNot(BeEmpty())

					// Key should never have equals
					Ω(k).ShouldNot(ContainSubstring("="))

					// Value may be empty, but let's track non-empty values
					if v != "" {
						valueCount++
					}
				}

				// Ensure we get at least one value from the environment
				Ω(valueCount).Should(BeNumerically(">", 0))
			})
		})
	})
})
