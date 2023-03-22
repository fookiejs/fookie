import * as lodash from "lodash"
import { models, run } from "../../..//core"
import { Read, Delete, Create, Count, Update } from "../../../method"
import { LifecycleFunction } from "../../../../types"

const reactives: LifecycleFunction = async function (payload, state) {
    const schema = payload.model.schema
    const fields = lodash.keys(schema)

    for (const field of fields) {
        if (lodash.has(schema[field], "reactives")) {
            for (const reactive of schema[field].reactives) {
                const entities = await run({
                    model: payload.model,
                    method: Read,
                    query: payload.query,
                })
                for (const entity of entities.data) {
                    if (entity[field]) {
                        await run({
                            model: schema[field].relation,
                            method: Update,
                            query: {
                                filter: {
                                    pk: entity[field],
                                },
                            },
                            body: {
                                [reactive.to]: reactive.compute(payload.body[reactive.from]),
                            },
                        })
                    }
                }
            }
        }
    }
}

export default reactives
