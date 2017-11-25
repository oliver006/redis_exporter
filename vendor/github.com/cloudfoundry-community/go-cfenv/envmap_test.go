package cfenv

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Envmap", func() {
	Describe("Environment variables should be split correctly", func() {
		test := func(input string, expectedKey string, expectedValue string) {
			k, v := splitEnv(input)
			Ω(k).Should(Equal(expectedKey))
			Ω(v).Should(Equal(expectedValue))
		}

		Context("With empty env var", func() {
			It("Should have empty value", func() {
				test("", "", "")
			})
		})

		Context("With env var not split by equals", func() {
			It("Should have empty value", func() {
				test("TEST", "TEST", "")
			})
		})

		Context("With env var split by equals but no value", func() {
			It("Should have empty value", func() {
				test("TEST=", "TEST", "")
			})
		})

		Context("With env var split by equals with key and value", func() {
			It("Should have non-empty key and value", func() {
				test("TEST=VAL", "TEST", "VAL")
			})
		})

		Context("With env var split by equals with key and value containing equals", func() {
			It("Should have non-empty key and value", func() {
				test("TEST=VAL=OTHERVAL", "TEST", "VAL=OTHERVAL")
			})
		})
	})
})
