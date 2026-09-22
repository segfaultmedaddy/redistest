package sliceutil_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.segfaultmedaddy.com/redistest/internal/sliceutil"
)

func Test_Set_NewSet(t *testing.T) {
	t.Parallel()

	// Arrange
	expectedValues := []string{}

	// Act
	actualSet := sliceutil.NewSet[string]()
	actualValues := actualSet.Values()

	// Assert
	assert.NotNil(t, actualSet)
	assert.Equal(t, expectedValues, actualValues)
}

func Test_Set_Has(t *testing.T) {
	t.Parallel()

	t.Run("should return false when the value has not been added", func(t *testing.T) {
		t.Parallel()

		// Arrange
		set := sliceutil.NewSet[string]()
		isValueExpected := false

		// Act
		hasValue := set.Has("value")

		// Assert
		assert.Equal(t, isValueExpected, hasValue)
	})

	t.Run("should return true when the value has been added", func(t *testing.T) {
		t.Parallel()

		// Arrange
		set := sliceutil.NewSet[string]()
		set.Add("value")

		isValueExpected := true

		// Act
		hasValue := set.Has("value")

		// Assert
		assert.Equal(t, isValueExpected, hasValue)
	})
}

func Test_Set_Add(t *testing.T) {
	t.Parallel()

	// Arrange
	set := sliceutil.NewSet[string]()
	expectedValues := []string{"value"}

	// Act
	set.Add("value")
	set.Add("value")
	actualValues := set.Values()

	// Assert
	assert.ElementsMatch(t, expectedValues, actualValues)
}

func Test_Set_Remove(t *testing.T) {
	t.Parallel()

	t.Run("should remove a value when it exists", func(t *testing.T) {
		t.Parallel()

		// Arrange
		set := sliceutil.NewSet[string]()
		set.Add("one")
		set.Add("two")

		expectedValues := []string{"two"}

		// Act
		set.Remove("one")
		actualValues := set.Values()

		// Assert
		assert.ElementsMatch(t, expectedValues, actualValues)
	})

	t.Run("should preserve the values when the removed value does not exist", func(t *testing.T) {
		t.Parallel()

		// Arrange
		set := sliceutil.NewSet[string]()
		set.Add("one")

		expectedValues := []string{"one"}

		// Act
		set.Remove("two")
		actualValues := set.Values()

		// Assert
		assert.ElementsMatch(t, expectedValues, actualValues)
	})
}

func Test_Set_Values(t *testing.T) {
	t.Parallel()

	t.Run("should return no values when the set is empty", func(t *testing.T) {
		t.Parallel()

		// Arrange
		set := sliceutil.NewSet[int]()
		expectedValues := []int{}

		// Act
		actualValues := set.Values()

		// Assert
		assert.Equal(t, expectedValues, actualValues)
	})

	t.Run("should return every distinct value when the set has values", func(t *testing.T) {
		t.Parallel()

		// Arrange
		set := sliceutil.NewSet[int]()
		set.Add(1)
		set.Add(2)
		set.Add(3)

		expectedValues := []int{1, 2, 3}

		// Act
		actualValues := set.Values()

		// Assert
		assert.ElementsMatch(t, expectedValues, actualValues)
	})
}
