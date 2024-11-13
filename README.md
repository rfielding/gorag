Golang RAG vs OpenAI example
============================

Do simple RAG from inside of Golang code,
by just using the OpenAI endpoint.

- OPENAI\_API\_KEY should be set in your environment
- you should have a postgres database setup
- I use go 1.22

Ask a question about the standard postgres `world` database.

```bash
(base) > go run main.go -dbname world -prompt "what is string(gnp) for countries top 10"

2024/11/13 01:53:06 Connected to database
2024/11/13 01:53:06 Retrieved schema
2024/11/13 01:53:06 Loaded metadata
2024/11/13 01:53:08 Got SQL query: SELECT name, CAST(gnp AS TEXT) AS gnp_string FROM country ORDER BY gnp DESC LIMIT 10;
2024/11/13 01:53:12 
%
name: United States
gnp_string: 8510700.00
name: Japan
gnp_string: 3787042.00
name: Germany
gnp_string: 2133367.00
name: France
gnp_string: 1424285.00
name: United Kingdom
gnp_string: 1378330.00
name: Italy
gnp_string: 1161755.00
name: China
gnp_string: 982268.00
name: Brazil
gnp_string: 776739.00
name: Canada
gnp_string: 598862.00
name: Spain
gnp_string: 553233.00
2024/11/13 01:53:12 The user prompt is asking for the Gross National Product (GNP) of the top 10 countries by GNP, presented as a string. The resulting query appears to have correctly retrieved the GNP values for the top 10 countries, converting them into string format with two decimal places. 

From the schema provided, the `country` table includes a `gnp` column, which stores the GNP values. The query likely ordered the countries by their GNP in descending order and selected the top 10 entries. The result is a list of country names along with their GNP values formatted as strings.

If you want to know how to write the SQL query that produced this result, it would look something like this:

---sql
SELECT name, CAST(gnp AS CHAR) AS gnp_string
FROM country
ORDER BY gnp DESC
LIMIT 10;
---

This SQL statement selects the `name` and `gnp` columns from the `country` table, casts the `gnp` values to a string, orders the results by `gnp` in descending order, and limits the results to the top 10 countries.
```

