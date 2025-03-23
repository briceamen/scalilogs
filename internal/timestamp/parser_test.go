package timestamp

import (
	"context"
	"testing"
)

func TestValidateAndNormalize(t *testing.T) {
	ctx := context.Background()

	t.Run("standard format", func(t *testing.T) {
		input := "2023-05-10 14:30:45"
		expected := "2023-05-10 14:30:45"

		result, err := ValidateAndNormalize(ctx, input)
		if err != nil {
			t.Fatalf("ValidateAndNormalize failed: %v", err)
		}

		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("today at format", func(t *testing.T) {
		input := "today at 14:30"

		result, err := ValidateAndNormalize(ctx, input)
		if err != nil {
			t.Fatalf("ValidateAndNormalize failed: %v", err)
		}

		if len(result) != 19 || result[11:] != "14:30:00" {
			t.Errorf("Expected result ending with '14:30:00', got %q", result)
		}
	})

	t.Run("YYYY-MM-DD at HH:MM:SS format", func(t *testing.T) {
		input := "2025-03-22 at 12:00:00"
		expected := "2025-03-22 12:00:00"

		result, err := ValidateAndNormalize(ctx, input)
		if err != nil {
			t.Fatalf("ValidateAndNormalize failed: %v", err)
		}

		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("YYYY-MM-DD at HH:MM format", func(t *testing.T) {
		input := "2025-03-22 at 12:00"
		expected := "2025-03-22 12:00:00"

		result, err := ValidateAndNormalize(ctx, input)
		if err != nil {
			t.Fatalf("ValidateAndNormalize failed: %v", err)
		}

		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("YYYY-MM-DD at HH format", func(t *testing.T) {
		input := "2025-03-22 at 12"
		expected := "2025-03-22 12:00:00"

		result, err := ValidateAndNormalize(ctx, input)
		if err != nil {
			t.Fatalf("ValidateAndNormalize failed: %v", err)
		}

		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("invalid date format", func(t *testing.T) {
		input := "2025-99-99 at 12:00:00"

		_, err := ValidateAndNormalize(ctx, input)
		if err == nil {
			t.Fatal("Expected error for invalid date, got nil")
		}
	})

	t.Run("invalid time format", func(t *testing.T) {
		input := "2025-03-22 at 99:99"

		_, err := ValidateAndNormalize(ctx, input)
		if err == nil {
			t.Fatal("Expected error for invalid time, got nil")
		}
	})
}
