package tests

/*
strPtr returns a pointer to a copy of a string.

* @brief: strPtr -> returns a pointer to a string copy
* @params: value [string] -> text to store
* @returns: out [*string] -> pointer to a copy
*/
func strPtr(value string) *string {
	return &value
}
