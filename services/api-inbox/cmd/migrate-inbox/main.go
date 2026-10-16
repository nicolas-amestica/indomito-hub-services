package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type options struct {
	command, sourceProfile, sourceRegion, sourceTable  string
	targetProfile, targetRegion, targetTable, manifest string
}

type manifest struct {
	Version           int            `json:"version"`
	CreatedAt         string         `json:"createdAt"`
	Source            string         `json:"source"`
	Target            string         `json:"target"`
	NeutralHash       string         `json:"neutralHash"`
	ConversationCount int            `json:"conversationCount"`
	MessageCount      int            `json:"messageCount"`
	Items             []manifestItem `json:"items"`
}

type manifestItem struct {
	PK   string                          `json:"pk"`
	SK   string                          `json:"sk"`
	Hash string                          `json:"hash"`
	Data json.RawMessage                 `json:"data"`
	Item map[string]types.AttributeValue `json:"-"`
}

func main() {
	opts := parseOptions()
	if err := run(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, "migrate-inbox:", err)
		os.Exit(1)
	}
}

func parseOptions() options {
	var o options
	if len(os.Args) < 3 || os.Args[1] != "legacy-to-target" {
		fmt.Fprintln(os.Stderr, "usage: migrate-inbox legacy-to-target <dry-run|apply|verify|rollback> [flags]")
		os.Exit(2)
	}
	o.command = os.Args[2]
	flags := flag.NewFlagSet("migrate-inbox", flag.ExitOnError)
	flags.StringVar(&o.sourceProfile, "source-profile", "root-pa", "AWS profile for legacy")
	flags.StringVar(&o.sourceRegion, "source-region", "us-west-2", "AWS region for legacy")
	flags.StringVar(&o.sourceTable, "source-table", "conversations-dev-pri-usw2", "legacy table")
	flags.StringVar(&o.targetProfile, "target-profile", "pa-dev", "AWS profile for target")
	flags.StringVar(&o.targetRegion, "target-region", "us-east-1", "AWS region for target")
	flags.StringVar(&o.targetTable, "target-table", "ind-dev-conversaciones-ddb-dev-pri-use1", "target table")
	flags.StringVar(&o.manifest, "manifest", ".migration/inbox-dev-manifest.json", "protected manifest path")
	_ = flags.Parse(os.Args[3:])
	return o
}

func awsClient(ctx context.Context, profile, region string) (*dynamodb.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile(profile), config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return dynamodb.NewFromConfig(cfg), nil
}

func run(ctx context.Context, o options) error {
	source, err := awsClient(ctx, o.sourceProfile, o.sourceRegion)
	if err != nil {
		return err
	}
	target, err := awsClient(ctx, o.targetProfile, o.targetRegion)
	if err != nil {
		return err
	}
	if o.command == "rollback" {
		return rollback(ctx, target, o)
	}
	m, err := buildManifest(ctx, source, o)
	if err != nil {
		return err
	}
	switch o.command {
	case "dry-run":
		fmt.Printf("dry-run ok: conversations=%d messages=%d items=%d hash=%s\n", m.ConversationCount, m.MessageCount, len(m.Items), m.NeutralHash)
		return nil
	case "apply":
		if err = apply(ctx, target, o.targetTable, &m); err != nil {
			return err
		}
		if err = writeManifest(o.manifest, m); err != nil {
			return err
		}
		fmt.Printf("apply ok: items=%d manifest=%s hash=%s\n", len(m.Items), o.manifest, m.NeutralHash)
		return verify(ctx, target, o.targetTable, m)
	case "verify":
		stored, err := readManifest(o.manifest)
		if err != nil {
			return err
		}
		if stored.NeutralHash != m.NeutralHash {
			return errors.New("source differs from protected manifest")
		}
		return verify(ctx, target, o.targetTable, stored)
	default:
		return fmt.Errorf("unknown command %q", o.command)
	}
}

func buildManifest(ctx context.Context, source *dynamodb.Client, o options) (manifest, error) {
	rows, err := queryAll(ctx, source, o.sourceTable, "CHATS")
	if err != nil {
		return manifest{}, err
	}
	items := []manifestItem{}
	messageCount := 0
	sequence := uint64(0)
	for _, row := range rows {
		phone := stringValue(row, "phone")
		if phone == "" {
			phone = strings.TrimPrefix(stringValue(row, "sk"), "CONV#")
		}
		if phone == "" {
			continue
		}
		conversationRows, qerr := queryAll(ctx, source, o.sourceTable, "CONV#"+phone)
		if qerr != nil {
			return manifest{}, qerr
		}
		sequence++
		now := firstString(row, "lastMessageAt", "updatedAt", "createdAt")
		if now == "" {
			now = time.Unix(0, 0).UTC().Format(time.RFC3339)
		}
		alias := firstString(row, "name", "alias", "contactName")
		if alias == "" {
			alias = phone
		}
		preview := firstString(row, "lastMessage", "lastMessageText")
		summary := map[string]types.AttributeValue{
			"id": s(phone), "phone": s(phone), "alias": s(alias), "channel": s("whatsapp"), "status": s(firstOr(stringValue(row, "status"), "bot")),
			"unreadCount": n(firstOr(numberValue(row, "unreadCount"), "0")), "lastMessagePreview": s(preview), "lastMessageAt": s(now), "createdAt": s(firstOr(stringValue(row, "createdAt"), now)), "updatedAt": s(now), "generation": n("1"),
		}
		items = append(items, makeItem("CONV#"+phone, "META", summary))
		items = append(items, makeItem(fmt.Sprintf("LIST_VERSION#%02X", shard(phone)), fmt.Sprintf("PHONE#%s#SEQ#%020d", phone, sequence), map[string]types.AttributeValue{"phone": s(phone), "validFromSequence": n(fmt.Sprint(sequence)), "operation": s("upsert"), "summary": &types.AttributeValueMemberM{Value: summary}}))
		items = append(items, makeItem("SEQ#DETAIL#"+phone, "META", map[string]types.AttributeValue{"sequence": n("0")}))
		for _, legacy := range conversationRows {
			sk := stringValue(legacy, "sk")
			if sk == "CONV#"+phone {
				continue
			}
			encoded, merr := attributevalue.MarshalMap(map[string]any{"id": strings.TrimPrefix(sk, "MSG#"), "phone": phone, "type": firstOr(stringValue(legacy, "type"), "customer"), "content": firstString(legacy, "content", "text"), "createdAt": firstString(legacy, "createdAt", "timestamp"), "attachments": []any{}})
			if merr != nil {
				return manifest{}, merr
			}
			items = append(items, makeItem("CONV#"+phone, sk, encoded))
			messageCount++
		}
	}
	items = append(items, makeItem("SEQ#LIST", "META", map[string]types.AttributeValue{"sequence": n(fmt.Sprint(sequence))}))
	items = append(items, makeItem("CONV_COUNT", "META", map[string]types.AttributeValue{"activeCount": n(fmt.Sprint(len(rows)))}))
	sort.Slice(items, func(i, j int) bool { return items[i].PK+"\x00"+items[i].SK < items[j].PK+"\x00"+items[j].SK })
	h := sha256.New()
	for i := range items {
		items[i].Hash = hashItem(items[i].Item)
		_, _ = h.Write([]byte(items[i].PK + "\x00" + items[i].SK + "\x00" + items[i].Hash + "\n"))
	}
	return manifest{Version: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), Source: o.sourceProfile + ":" + o.sourceRegion + ":" + o.sourceTable, Target: o.targetProfile + ":" + o.targetRegion + ":" + o.targetTable, NeutralHash: hex.EncodeToString(h.Sum(nil)), ConversationCount: len(rows), MessageCount: messageCount, Items: items}, nil
}

func queryAll(ctx context.Context, client *dynamodb.Client, table, pk string) ([]map[string]types.AttributeValue, error) {
	var result []map[string]types.AttributeValue
	var start map[string]types.AttributeValue
	for {
		out, err := client.Query(ctx, &dynamodb.QueryInput{TableName: &table, KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(pk)}, ExclusiveStartKey: start, ConsistentRead: aws.Bool(true)})
		if err != nil {
			return nil, fmt.Errorf("query %s: %w", pk, err)
		}
		result = append(result, out.Items...)
		if len(out.LastEvaluatedKey) == 0 {
			return result, nil
		}
		start = out.LastEvaluatedKey
	}
}

func apply(ctx context.Context, client *dynamodb.Client, table string, m *manifest) error {
	for i := range m.Items {
		item := m.Items[i]
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{TableName: &table, Item: item.Item, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")})
		if err != nil {
			return fmt.Errorf("write %s/%s: %w", item.PK, item.SK, err)
		}
	}
	return nil
}

func verify(ctx context.Context, client *dynamodb.Client, table string, m manifest) error {
	for _, expected := range m.Items {
		out, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &table, Key: map[string]types.AttributeValue{"pk": s(expected.PK), "sk": s(expected.SK)}, ConsistentRead: aws.Bool(true)})
		if err != nil {
			return err
		}
		if hashItem(out.Item) != expected.Hash {
			return fmt.Errorf("checksum mismatch %s/%s", expected.PK, expected.SK)
		}
	}
	fmt.Printf("verify ok: items=%d hash=%s\n", len(m.Items), m.NeutralHash)
	return nil
}

func rollback(ctx context.Context, client *dynamodb.Client, o options) error {
	m, err := readManifest(o.manifest)
	if err != nil {
		return err
	}
	if m.Target != o.targetProfile+":"+o.targetRegion+":"+o.targetTable {
		return errors.New("manifest target mismatch")
	}
	for _, expected := range m.Items {
		out, gerr := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &o.targetTable, Key: map[string]types.AttributeValue{"pk": s(expected.PK), "sk": s(expected.SK)}, ConsistentRead: aws.Bool(true)})
		if gerr != nil {
			return gerr
		}
		if len(out.Item) == 0 {
			continue
		}
		if hashItem(out.Item) != expected.Hash {
			return fmt.Errorf("refuse rollback: item changed %s/%s", expected.PK, expected.SK)
		}
		_, derr := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &o.targetTable, Key: map[string]types.AttributeValue{"pk": s(expected.PK), "sk": s(expected.SK)}})
		if derr != nil {
			return derr
		}
	}
	fmt.Printf("rollback ok: deleted manifest-owned items=%d\n", len(m.Items))
	return nil
}

func makeItem(pk, sk string, fields map[string]types.AttributeValue) manifestItem {
	item := map[string]types.AttributeValue{"pk": s(pk), "sk": s(sk)}
	for k, v := range fields {
		item[k] = v
	}
	data := canonicalItem(item)
	return manifestItem{PK: pk, SK: sk, Data: data, Item: item}
}
func canonicalItem(item map[string]types.AttributeValue) json.RawMessage {
	var normalized map[string]any
	_ = attributevalue.UnmarshalMap(item, &normalized)
	raw, _ := json.Marshal(normalized)
	return raw
}
func hashItem(item map[string]types.AttributeValue) string {
	sum := sha256.Sum256(canonicalItem(item))
	return hex.EncodeToString(sum[:])
}
func writeManifest(path string, m manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0600)
}
func readManifest(path string) (manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	var m manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	return m, rehydrate(&m)
}
func rehydrate(m *manifest) error {
	for i := range m.Items {
		var neutral map[string]any
		if err := json.Unmarshal(m.Items[i].Data, &neutral); err != nil {
			return err
		}
		item, err := attributevalue.MarshalMap(neutral)
		if err != nil {
			return err
		}
		m.Items[i].Item = item
	}
	return nil
}
func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func n(v string) types.AttributeValue { return &types.AttributeValueMemberN{Value: v} }
func stringValue(m map[string]types.AttributeValue, k string) string {
	if v, ok := m[k].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}
func numberValue(m map[string]types.AttributeValue, k string) string {
	if v, ok := m[k].(*types.AttributeValueMemberN); ok {
		return v.Value
	}
	return ""
}
func firstString(m map[string]types.AttributeValue, ks ...string) string {
	for _, k := range ks {
		if v := stringValue(m, k); v != "" {
			return v
		}
	}
	return ""
}
func firstOr(v, f string) string {
	if v == "" {
		return f
	}
	return v
}
func shard(phone string) byte { sum := sha256.Sum256([]byte(phone)); return sum[0] & 15 }
