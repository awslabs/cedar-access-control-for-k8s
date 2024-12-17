package schema

// ModifyObjectMetaMaps modifies the ObjectMeta maps in the schema
func ModifyObjectMetaMaps(schema CedarSchema) {
	ns, ok := schema["meta::v1"]
	if !ok {
		return
	}
	keyValEntity := EntityShape{
		Type: RecordType,
		Attributes: map[string]EntityAttribute{
			"key":   {Type: StringType, Required: true},
			"value": {Type: StringType, Required: true},
		},
	}
	ns.CommonTypes["KeyValue"] = keyValEntity

	keyValStringSliceEntity := EntityShape{
		Type: RecordType,
		Attributes: map[string]EntityAttribute{
			"key":   {Type: StringType, Required: true},
			"value": {Type: SetType, Element: &EntityAttributeElement{Type: StringType}, Required: true},
		},
	}
	ns.CommonTypes["KeyValueStringSlice"] = keyValStringSliceEntity

	schema["meta::v1"] = ns
}
